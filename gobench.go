package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/MongoHQ/graphizer"
	"github.com/rcrowley/go-metrics"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	requests         int64
	period           int64
	clients          int
	url              string
	urlsFilePath     string
	keepAlive        bool
	postDataFilePath string
	connectTimeout   int
	writeTimeout     int
	readTimeout      int
	authCookie       string
	verboseMode      bool
	salt             string
	statsLog         *log.Logger
	statsLogLocation string
	adminPort        int
	graphiteServer   string
)

type Configuration struct {
	urls      []string
	method    string
	postData  []byte
	requests  int64
	period    int64
	keepAlive bool
}

type Result struct {
	requests        int64
	success         int64
	networkFailed   int64
	badFailed       int64
	readThroughput  int64
	writeThroughput int64
}

type MyConn struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
	result       *Result
}

func md5str(text string) string {
	h := md5.New()
	io.WriteString(h, text)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func generateAuthCookie() string {
	expiry := int32(time.Now().Add(time.Duration(10) * time.Hour).Unix())
	path := "/*"

	if salt == "" {
		log.Fatalf("Salt is not defined: %s\n", salt)
	}

	hash := md5str(fmt.Sprintf("%d%s%s", expiry, path, salt))
	return fmt.Sprintf("auth=expires=%d~access=%s~md5=%s", expiry, path, hash)
}

func (this *MyConn) Read(b []byte) (n int, err error) {
	len, err := this.Conn.Read(b)

	if err == nil {
		this.result.readThroughput += int64(len)
		this.Conn.SetReadDeadline(time.Now().Add(this.readTimeout))
	}

	return len, err
}

func (this *MyConn) Write(b []byte) (n int, err error) {
	len, err := this.Conn.Write(b)

	if err == nil {
		this.result.writeThroughput += int64(len)
		this.Conn.SetWriteDeadline(time.Now().Add(this.writeTimeout))
	}

	return len, err
}

func init() {
	flag.Int64Var(&requests, "r", -1, "Number of requests per client")
	flag.IntVar(&clients, "c", 100, "Number of concurrent clients")
	flag.StringVar(&url, "u", "", "URL")
	flag.StringVar(&urlsFilePath, "f", "", "URL's file path (line seperated)")
	flag.BoolVar(&keepAlive, "k", true, "Do HTTP keep-alive")
	flag.StringVar(&postDataFilePath, "d", "", "HTTP POST data file path")
	flag.Int64Var(&period, "t", -1, "Period of time (in seconds)")
	flag.IntVar(&connectTimeout, "tc", 5000, "Connect timeout (in milliseconds)")
	flag.IntVar(&writeTimeout, "tw", 5000, "Write timeout (in milliseconds)")
	flag.IntVar(&readTimeout, "tr", 5000, "Read timeout (in milliseconds)")
	flag.BoolVar(&verboseMode, "v", false, "Verbose mode")
	flag.StringVar(&salt, "s", "", "Auth salt")
	flag.StringVar(&statsLogLocation, "sl", "", "Stats log file location")
	flag.IntVar(&adminPort, "ap", 0, "Admin HTTP port")
	flag.StringVar(&graphiteServer, "gs", "", "Graphite server")
}

func printResults(results map[int]*Result, startTime time.Time) {
	var requests int64
	var success int64
	var networkFailed int64
	var badFailed int64
	var readThroughput int64
	var writeThroughput int64

	for _, result := range results {
		requests += result.requests
		success += result.success
		networkFailed += result.networkFailed
		badFailed += result.badFailed
		readThroughput += result.readThroughput
		writeThroughput += result.writeThroughput
	}

	elapsed := int64(time.Since(startTime).Seconds())

	if elapsed == 0 {
		elapsed = 1
	}

	log.Println("FINAL RESULTS")
	log.Printf("Requests:                       %10d hits\n", requests)
	log.Printf("Successful requests:            %10d hits\n", success)
	log.Printf("Network failed:                 %10d hits\n", networkFailed)
	log.Printf("Bad requests failed (!2xx):     %10d hits\n", badFailed)
	log.Printf("Successfull requests rate:      %10d hits/sec\n", success/elapsed)
	log.Printf("Read throughput:                %10d bytes/sec\n", readThroughput/elapsed)
	log.Printf("Write throughput:               %10d bytes/sec\n", writeThroughput/elapsed)
	log.Printf("Test time:                      %10d sec\n", elapsed)
}

func readLines(path string) (lines []string, err error) {
	var file *os.File
	var part []byte
	var prefix bool
	var reader *bufio.Reader

	if file, err = os.Open(path); err != nil {
		return
	}
	defer file.Close()

	if strings.HasSuffix(path, ".gz") {
		zhandle, err1 := gzip.NewReader(file)

		if err1 != nil {
			err = err1
			return
		}

		reader = bufio.NewReader(zhandle)
	} else {
		reader = bufio.NewReader(file)
	}

	buffer := bytes.NewBuffer(make([]byte, 0))

	for {
		if part, prefix, err = reader.ReadLine(); err != nil {
			break
		}
		buffer.Write(part)
		if !prefix {
			lines = append(lines, buffer.String())
			buffer.Reset()
		}
	}

	if err == io.EOF {
		err = nil
	}

	return
}

func NewConfiguration() *Configuration {

	if urlsFilePath == "" && url == "" {
		flag.Usage()
		os.Exit(1)
	}

	if requests == -1 && period == -1 {
		fmt.Println("Requests or period must be provided")
		flag.Usage()
		os.Exit(1)
	}

	if requests != -1 && period != -1 {
		fmt.Println("Only one should be provided: [requests|period]")
		flag.Usage()
		os.Exit(1)
	}

	configuration := &Configuration{
		urls:      make([]string, 0),
		method:    "GET",
		postData:  nil,
		keepAlive: keepAlive,
		requests:  int64((1 << 63) - 1)}

	if period != -1 {
		configuration.period = period

		timeout := make(chan bool, 1)
		go func() {
			<-time.After(time.Duration(period) * time.Second)
			timeout <- true
		}()

		go func() {
			<-timeout
			syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()
	}

	if requests != -1 {
		configuration.requests = requests
	}

	if urlsFilePath != "" {
		fileLines, err := readLines(urlsFilePath)

		if err != nil {
			log.Fatalf("Error in ioutil.ReadFile for file: %s Error: ", urlsFilePath, err)
		}

		configuration.urls = fileLines
	}

	if url != "" {
		configuration.urls = append(configuration.urls, url)
	}

	if postDataFilePath != "" {
		configuration.method = "POST"

		data, err := ioutil.ReadFile(postDataFilePath)

		if err != nil {
			log.Fatalf("Error in ioutil.ReadFile for file path: %s Error: ", postDataFilePath, err)
		}

		configuration.postData = data
	}

	return configuration
}

func TimeoutDialer(result *Result, connectTimeout, readTimeout, writeTimeout time.Duration) func(net, address string) (conn net.Conn, err error) {
	return func(mynet, address string) (net.Conn, error) {
		conn, err := net.DialTimeout(mynet, address, connectTimeout)
		if err != nil {
			return nil, err
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))

		myConn := &MyConn{Conn: conn, readTimeout: readTimeout, writeTimeout: writeTimeout, result: result}

		return myConn, nil
	}
}

func MyClient(result *Result, connectTimeout, readTimeout, writeTimeout time.Duration) *http.Client {

	return &http.Client{
		Transport: &http.Transport{
			Dial:            TimeoutDialer(result, connectTimeout, readTimeout, writeTimeout),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func client(configuration *Configuration, result *Result, done *sync.WaitGroup) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("caught recover: ", r)
			os.Exit(1)
		}
	}()

	processRequest := func(req *http.Request, myclient *http.Client) {
		if verboseMode {
			reqb, _ := httputil.DumpRequest(req, false)
			fmt.Printf("==== REQUEST ===\n%s==== END REQUEST ===\n", reqb)
		}

		resp, err := myclient.Do(req)

		result.requests++

		if err != nil {
			if verboseMode {
				fmt.Printf("Error connecting %s\n", err)
			}

			count := metrics.GetOrRegisterCounter("net_connect_errors",
				metrics.DefaultRegistry)

			count.Inc(1)

			result.networkFailed++
			return
		}

		_, errRead := ioutil.ReadAll(resp.Body)

		if errRead != nil {
			if verboseMode {
				fmt.Printf("Error reading %s\n", errRead)
			}

			result.networkFailed++

			count := metrics.GetOrRegisterCounter("net_read_errors",
				metrics.DefaultRegistry)
			count.Inc(1)

			return
		}

		if resp.StatusCode == http.StatusOK {
			result.success++
		} else {
			result.badFailed++
		}

		cnt := metrics.GetOrRegisterCounter(strconv.Itoa(resp.StatusCode), metrics.DefaultRegistry)
		cnt.Inc(1)

		if verboseMode {
			respb, _ := httputil.DumpResponse(resp, false)
			fmt.Printf("=== RESPONSE ===\n%s=== END RESPONSE ====\n", respb)
		}

		resp.Body.Close()
	}

	latencyTimer := metrics.GetOrRegisterTimer("latency_timer", metrics.DefaultRegistry)

	myclient := MyClient(result, time.Duration(connectTimeout)*time.Millisecond,
		time.Duration(readTimeout)*time.Millisecond,
		time.Duration(writeTimeout)*time.Millisecond)

	for result.requests < configuration.requests {
		for _, tmpUrl := range configuration.urls {
			req, _ := http.NewRequest(configuration.method, tmpUrl, bytes.NewReader(configuration.postData))

			if configuration.keepAlive == true {
				req.Header.Add("Connection", "keep-alive")
			} else {
				req.Header.Add("Connection", "close")
			}

			req.Header.Add("Accept-encoding", "gzip")
			req.Header.Add("Cookie", authCookie)

			latencyTimer.Time(func() { processRequest(req, myclient) })
		}
	}

	done.Done()
}

// Output each metric in the given registry periodically using the given
// logger.
func logMetrics(r metrics.Registry, d time.Duration) {
	for {
		time.Sleep(d)
		for _, s := range dumpMetrics(r) {
			statsLog.Println(s)
		}
	}
}

func makeMetricsMap(r metrics.Registry) map[string]map[string]interface{} {
	fms := func(d float64) int64 {
		return int64(d / float64(time.Millisecond))
	}

	ims := func(d int64) int64 {
		return d / int64(time.Millisecond)
	}

	out := make(map[string]map[string]interface{})

	r.Each(func(name string, i interface{}) {
		switch m := i.(type) {

		case metrics.Counter:
			if out["counters"] == nil {
				out["counters"] = make(map[string]interface{})
			}
			out["counters"][name] = m.Count()

		// case metrics.Gauge:
		// 	result = append(result, fmt.Sprintf("gauge/%s: %d", name, m.Value()))

		// case metrics.Healthcheck:
		// 	m.Check()
		// 	result = append(result, fmt.Sprintf("healthcheck/%s: %v", name, m.Error()))

		// case metrics.Histogram:
		// 	ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})
		// 	prefix := fmt.Sprintf("histogram %s:", name)

		// 	statsLog.Printf("%s count: %d, min: %d, max: %d\n",
		// 		prefix, m.Count(), m.Min(), m.Max())

		// 	statsLog.Printf("%s mean: %.2f, stddev: %.2f, median: %.2f\n",
		// 		prefix, ms(m.Mean()), ms(m.StdDev()), ms(ps[0]))

		// 	statsLog.Printf("%s 75%%: %.2f, 95%%: %.2f, 99%%: %.2f, 99.9%%: %.2f\n",
		// 		prefix, ms(ps[1]), ms(ps[2]), ms(ps[3]), ms(ps[4]))

		// case metrics.Meter:
		// 	prefix := fmt.Sprintf("meter %s:", name)

		// 	statsLog.Printf("%s count: %d, rate: %.2f, %.2f, %.2f, mean rate: %.2f\n",
		// 		prefix, m.Count(), m.Rate1(), m.Rate5(), m.Rate15(), m.RateMean())

		case metrics.Timer:
			if out["metrics"] == nil {
				out["metrics"] = make(map[string]interface{})
			}

			tm := make(map[string]interface{})
			out["metrics"][name] = tm

			ps := m.Percentiles([]float64{0.5, 0.90, 0.95, 0.99, 0.999})
			tm["average"] = fms(m.Mean())
			tm["count"] = m.Count()
			tm["maximum"] = ims(m.Max())
			tm["minimum"] = ims(m.Min())
			tm["p50"] = fms(ps[0])
			tm["p90"] = fms(ps[1])
			tm["p95"] = fms(ps[2])
			tm["p99"] = fms(ps[3])
			tm["p999"] = fms(ps[4])
			tm["rate_1"] = int64(m.Rate1())
			tm["rate_5"] = int64(m.Rate5())
			tm["rate_15"] = int64(m.Rate15())
			tm["rate_mean"] = int64(m.RateMean())

		default:
			log.Fatalf("unsupported metric type for \"%s\"", name)
		}
	})

	return out
}

func dumpMetricsJson(r metrics.Registry) (result []byte) {
	result, _ = json.Marshal(makeMetricsMap(r))
	return
}

func dumpMetrics(r metrics.Registry) (result []string) {
	fms := func(d float64) float64 {
		return d / float64(time.Millisecond)
	}

	ims := func(d int64) int64 {
		return d / int64(time.Millisecond)
	}

	r.Each(func(name string, i interface{}) {
		switch m := i.(type) {

		case metrics.Counter:
			result = append(result, fmt.Sprintf("counter/%s: %d", name, m.Count()))

		// case metrics.Gauge:
		// 	result = append(result, fmt.Sprintf("gauge/%s: %d", name, m.Value()))

		// case metrics.Healthcheck:
		// 	m.Check()
		// 	result = append(result, fmt.Sprintf("healthcheck/%s: %v", name, m.Error()))

		// case metrics.Histogram:
		// 	ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})
		// 	prefix := fmt.Sprintf("histogram %s:", name)

		// 	statsLog.Printf("%s count: %d, min: %d, max: %d\n",
		// 		prefix, m.Count(), m.Min(), m.Max())

		// 	statsLog.Printf("%s mean: %.2f, stddev: %.2f, median: %.2f\n",
		// 		prefix, ms(m.Mean()), ms(m.StdDev()), ms(ps[0]))

		// 	statsLog.Printf("%s 75%%: %.2f, 95%%: %.2f, 99%%: %.2f, 99.9%%: %.2f\n",
		// 		prefix, ms(ps[1]), ms(ps[2]), ms(ps[3]), ms(ps[4]))

		// case metrics.Meter:
		// 	prefix := fmt.Sprintf("meter %s:", name)

		// 	statsLog.Printf("%s count: %d, rate: %.2f, %.2f, %.2f, mean rate: %.2f\n",
		// 		prefix, m.Count(), m.Rate1(), m.Rate5(), m.Rate15(), m.RateMean())

		case metrics.Timer:
			ps := m.Percentiles([]float64{0.5, 0.90, 0.95, 0.99, 0.999})
			result = append(result, fmt.Sprintf("timer/%s_ms: (average=%.0f, stdev=%.0f, count=%d, "+
				"maximum=%d, minimum=%d, p50=%.0f, p90=%.0f, p95=%.0f, p99=%.0f, p999=%.0f, "+
				"rate_1: %.0f, rate_5: %.0f, rate_15: %.0f, mean_rate: %.0f)",
				name, fms(m.Mean()), fms(m.StdDev()), m.Count(), ims(m.Max()), ims(m.Min()),
				fms(ps[0]), fms(ps[1]), fms(ps[2]), fms(ps[3]), fms(ps[4]),
				m.Rate1(), m.Rate5(), m.Rate15(), m.RateMean()))

		default:
			log.Fatalf("unsupported metric type for \"%s\"", name)
		}
	})

	sort.Strings(result)

	return
}

func startAdminServer() {
	http.HandleFunc("/stats.txt", func(w http.ResponseWriter, req *http.Request) {
		for _, s := range dumpMetrics(metrics.DefaultRegistry) {
			fmt.Fprintln(w, s)
		}
	})

	http.HandleFunc("/stats.json", func(w http.ResponseWriter, req *http.Request) {
		w.Write(dumpMetricsJson(metrics.DefaultRegistry))
	})

	http.HandleFunc("/pid.txt", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, syscall.Getpid())
	})

	http.HandleFunc("/ping.txt", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "pong")
	})

	http.HandleFunc("/shutdown", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "bye")
		timer := time.NewTimer(time.Second)
		go func() {
			<-timer.C
			log.Println("shutting down per admin request")
			syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()
	})

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", adminPort), nil))
}

func startStatsLogging() *os.File {
	var f *os.File
	var err error

	// real /dev/stderr does not work well when you su into another user
	if statsLogLocation == "STDERR" {
		f = os.Stderr
	} else {
		f, err = os.OpenFile(statsLogLocation, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("error opening file: %v", err)
		}
	}

	statsLog = log.New(f, "", log.LstdFlags)

	go logMetrics(metrics.DefaultRegistry, time.Duration(1)*time.Second)

	return f
}

func startGraphiteReporting() {
	d := time.Duration(10) * time.Second
	g := graphizer.NewGraphite(graphizer.TCP, graphiteServer)
	h, err := os.Hostname()

	if err != nil {
		log.Fatalf("can't get my hostname: %v", err)
	}

	// Graphite uses periods to delimit nodes
	graphite_host := strings.Replace(h, ".", "_", -1)

	send := func(name string, value interface{}) {
		full_name := graphite_host + ".generator." + name
		// log.Printf("SENDING %s = %v to %v", full_name, value, g)
		g.Send(graphizer.Metric{full_name, value, time.Now().Unix()})
	}

	for {
		time.Sleep(d)

		m := makeMetricsMap(metrics.DefaultRegistry)

		for key, value := range m["counters"] {
			send(key, value)
		}

		for metric, metric_map := range m["metrics"] {
			for key, value := range metric_map.(map[string]interface{}) {
				send(metric+"."+key, value)
			}
		}
	}

}

func main() {
	startTime := time.Now()

	var done sync.WaitGroup
	results := make(map[int]*Result)

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)
	go func() {
		_ = <-signalChannel
		printResults(results, startTime)
		os.Exit(0)
	}()

	flag.Parse()

	configuration := NewConfiguration()

	goMaxProcs := os.Getenv("GOMAXPROCS")

	if goMaxProcs == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	authCookie = generateAuthCookie()

	if statsLogLocation != "" {
		f := startStatsLogging()
		defer f.Close()
	}

	if adminPort != 0 {
		go startAdminServer()
	}

	if graphiteServer != "" {
		go startGraphiteReporting()
	}

	log.Printf("Dispatching %d clients\n", clients)

	done.Add(clients)
	for i := 0; i < clients; i++ {
		result := &Result{}
		results[i] = result
		go client(configuration, result, &done)

	}

	log.Println("Waiting for results...")

	done.Wait()

	printResults(results, startTime)
}
