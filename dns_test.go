package main

import (
	"math"
	"sort"
	"testing"
	"time"
)

// makes sure we get random adresses
func TestDns(t *testing.T) {
	ch := make(chan string)
	d, _ := time.ParseDuration("10s")
	go dns(ch, d, "google.com")

	host_map := make(map[string]int)
	for i := 0; i < 10000; i++ {
		host_map[<-ch]++
	}

	counts := make([]int, 0, len(host_map))
	for _, c := range host_map {
		counts = append(counts, c)
	}

	sort.Ints(counts)

	median := counts[len(counts)/2]

	for i := range counts {
		diff := math.Abs(float64(counts[i]-median)) / float64(median)
		if diff > 0.1 {
			t.Fatalf("uneven count: %d, median: %d, out of %v", counts[i], median, counts)
		}
	}
}
