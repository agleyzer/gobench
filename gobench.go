package main

import (
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"flag"
	"fmt"
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
	"strconv"
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
	generatorId      string
)

type Configuration struct {
	urls      chan []string
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
	flag.StringVar(&generatorId, "id", defaultGeneratorId(), "Generator id (e.g. for Graphite)")
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

func readLines(path string, out chan []string, length int) {
	reader := NewInfiniteLineReader(path)
	defer reader.Close()

	buf := make([]string, length)

	for {
		for i := 0; i < len(buf); i++ {
			buf[i] = reader.NextLine()
		}
		out <- buf
	}
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
		urls:      make(chan []string, clients),
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
		go readLines(urlsFilePath, configuration.urls, 1000)
	}

	if url != "" {
		go func() {
			buffer := make([]string, 1000)
			for i := 0; i < len(buffer); i++ {
				buffer[i] = url
			}
			for {
				configuration.urls <- buffer
			}
		}()
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

	var urls []string

	for i := 0; result.requests < configuration.requests; i++ {
		// get more urls if we ran out
		if urls == nil || i >= len(urls) {
			urls = <-configuration.urls
			i = 0
		}

		req, _ := http.NewRequest(configuration.method, urls[i], bytes.NewReader(configuration.postData))

		if configuration.keepAlive == true {
			req.Header.Add("Connection", "keep-alive")
		} else {
			req.Header.Add("Connection", "close")
		}

		req.Header.Add("Accept-encoding", "gzip")
		req.Header.Add("Cookie", authCookie)

		latencyTimer.Time(func() { processRequest(req, myclient) })
	}

	done.Done()
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
