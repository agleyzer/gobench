package main

import (
	"bufio"
	"fmt"
	"github.com/rcrowley/go-metrics"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

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

func dumpMetrics(r metrics.Registry) (result []string) {
	fms := func(d float64) float64 {
		return d / float64(time.Millisecond)
	}

	ims := func(d int64) int64 {
		return d / int64(time.Millisecond)
	}

	r.Each(func(name string, i interface{}) {
		switch metric := i.(type) {
		case metrics.Counter:
			result = append(result, fmt.Sprintf("%s.count %d", name, metric.Count()))
		case metrics.Gauge:
			result = append(result, fmt.Sprintf("%s.value %d", name, metric.Value()))
		case metrics.Histogram:
			h := metric.Snapshot()
			ps := h.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})
			result = append(result, fmt.Sprintf("%s.count %d", name, h.Count()))
			result = append(result, fmt.Sprintf("%s.min %d", name, h.Min()))
			result = append(result, fmt.Sprintf("%s.max %d", name, h.Max()))
			result = append(result, fmt.Sprintf("%s.mean %.2f", name, h.Mean()))
			result = append(result, fmt.Sprintf("%s.std-dev %.2f", name, h.StdDev()))
			result = append(result, fmt.Sprintf("%s.50-percentile %.2f", name, ps[0]))
			result = append(result, fmt.Sprintf("%s.75-percentile %.2f", name, ps[1]))
			result = append(result, fmt.Sprintf("%s.95-percentile %.2f", name, ps[2]))
			result = append(result, fmt.Sprintf("%s.99-percentile %.2f", name, ps[3]))
			result = append(result, fmt.Sprintf("%s.999-percentile %.2f", name, ps[4]))
		case metrics.Meter:
			m := metric.Snapshot()
			result = append(result, fmt.Sprintf("%s.count %d", name, m.Count()))
			result = append(result, fmt.Sprintf("%s.one-minute %.2f", name, m.Rate1()))
			result = append(result, fmt.Sprintf("%s.five-minute %.2f", name, m.Rate5()))
			result = append(result, fmt.Sprintf("%s.fifteen-minute %.2f", name, m.Rate15()))
			result = append(result, fmt.Sprintf("%s.mean %.2f", name, m.RateMean()))
		case metrics.Timer:
			t := metric.Snapshot()
			ps := t.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})
			result = append(result, fmt.Sprintf("%s.count %d", name, t.Count()))
			result = append(result, fmt.Sprintf("%s.min %d", name, ims(t.Min())))
			result = append(result, fmt.Sprintf("%s.max %d", name, ims(t.Max())))
			result = append(result, fmt.Sprintf("%s.mean %.2f", name, fms(t.Mean())))
			result = append(result, fmt.Sprintf("%s.std-dev %.2f", name, fms(t.StdDev())))
			result = append(result, fmt.Sprintf("%s.50-percentile %.2f", name, fms(ps[0])))
			result = append(result, fmt.Sprintf("%s.75-percentile %.2f", name, fms(ps[1])))
			result = append(result, fmt.Sprintf("%s.95-percentile %.2f", name, fms(ps[2])))
			result = append(result, fmt.Sprintf("%s.99-percentile %.2f", name, fms(ps[3])))
			result = append(result, fmt.Sprintf("%s.999-percentile %.2f", name, fms(ps[4])))
			result = append(result, fmt.Sprintf("%s.one-minute %.2f", name, t.Rate1()))
			result = append(result, fmt.Sprintf("%s.five-minute %.2f", name, t.Rate5()))
			result = append(result, fmt.Sprintf("%s.fifteen-minute %.2f", name, t.Rate15()))
			result = append(result, fmt.Sprintf("%s.mean-rate %.2f", name, t.RateMean()))
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

func defaultGeneratorId() string {
	h, err := os.Hostname()

	if err != nil {
		log.Fatalf("can't get my hostname: %v", err)
	}

	// Graphite uses periods to delimit nodes
	return strings.Replace(h, ".", "_", -1)
}

func startGraphiteReporting() {
	addr, err := net.ResolveTCPAddr("tcp", graphiteServer)
	if err != nil {
		log.Fatalf("Can't connect to graphite: %v", err)
	}

	send := func() error {
		now := time.Now().Unix()

		conn, err := net.DialTCP("tcp", nil, addr)
		if nil != err {
			return err
		}
		defer conn.Close()

		w := bufio.NewWriter(conn)

		for _, s := range dumpMetrics(metrics.DefaultRegistry) {
			fmt.Fprintf(w, "%s.generator.%s %d\n", generatorId, s, now)
		}

		w.Flush()
		return nil
	}

	for _ = range time.Tick(10 * time.Second) {
		if err := send(); nil != err {
			log.Printf("Graphite send failed: %v", err)
		}
	}
}
