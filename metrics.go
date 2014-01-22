package main

import (
	"encoding/json"
	"fmt"
	"github.com/MongoHQ/graphizer"
	"github.com/rcrowley/go-metrics"
	"log"
	"net/http"
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

func defaultGeneratorId() string {
	h, err := os.Hostname()

	if err != nil {
		log.Fatalf("can't get my hostname: %v", err)
	}

	// Graphite uses periods to delimit nodes
	return strings.Replace(h, ".", "_", -1)
}

func startGraphiteReporting() {
	d := time.Duration(10) * time.Second
	g := graphizer.NewGraphite(graphizer.TCP, graphiteServer)

	send := func(name string, value interface{}) {
		full_name := generatorId + ".generator." + name
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
