package main

import (
	"strings"
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
	"compress/gzip"
	"github.com/rcrowley/go-metrics"
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
		fmt.Printf("Salt is not defined: %s\n", salt)
		os.Exit(1)
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

	fmt.Println()
	fmt.Printf("Requests:                       %10d hits\n", requests)
	fmt.Printf("Successful requests:            %10d hits\n", success)
	fmt.Printf("Network failed:                 %10d hits\n", networkFailed)
	fmt.Printf("Bad requests failed (!2xx):     %10d hits\n", badFailed)
	fmt.Printf("Successfull requests rate:      %10d hits/sec\n", success/elapsed)
	fmt.Printf("Read throughput:                %10d bytes/sec\n", readThroughput/elapsed)
	fmt.Printf("Write throughput:               %10d bytes/sec\n", writeThroughput/elapsed)
	fmt.Printf("Test time:                      %10d sec\n", elapsed)
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

		if (err1 != nil) {
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

	processRequest := func (req *http.Request, myclient *http.Client) {
		if verboseMode {
			reqb, _ := httputil.DumpRequest(req, false)
			fmt.Printf("==== REQUEST ===\n%s==== END REQUEST ===\n", reqb)
		}

		resp, err := myclient.Do(req)

		result.requests++

		if err != nil {
			fmt.Printf("Error connecting %s\n", err)
			result.networkFailed++
			return
		}

		_, errRead := ioutil.ReadAll(resp.Body)

		if errRead != nil {
			fmt.Printf("Error reading %s\n", err)
			result.networkFailed++
			return
		}

		if resp.StatusCode == http.StatusOK {
			result.success++
		} else {
			result.badFailed++
		}

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
func LogLatency(r metrics.Registry, d time.Duration) {
	ms := func (d float64) float64 {
		return d / float64(time.Millisecond)
	}

	l := log.New(os.Stderr, "latency: ", log.Ldate | log.Ltime)

	for {
		time.Sleep(d)

		m := r.Get("latency_timer").(metrics.Timer)
		ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})

		l.Printf("count: %d, min: %d, max: %d\n", m.Count(), m.Min(), m.Max())
		l.Printf("mean: %.2f, stddev: %.2f, median: %.2f\n", ms(m.Mean()), ms(m.StdDev()), ms(ps[0]))
		l.Printf("75%%: %.2f, 95%%: %.2f, 99%%: %.2f, 99.9%%: %.2f\n", ms(ps[1]), ms(ps[2]), ms(ps[3]), ms(ps[4]))
		l.Printf("rate: %.2f, %.2f, %.2f, mean rate: %.2f\n",
			m.Rate1(), m.Rate5(), m.Rate15(), m.RateMean())
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

	go LogLatency(metrics.DefaultRegistry, time.Duration(1) * time.Second)

	fmt.Printf("Dispatching %d clients\n", clients)

	done.Add(clients)
	for i := 0; i < clients; i++ {
		result := &Result{}
		results[i] = result
		go client(configuration, result, &done)

	}
	fmt.Println("Waiting for results...")
	done.Wait()
	printResults(results, startTime)
}
