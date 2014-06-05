package main

import (
	"log"
	"math/rand"
	"net"
	"time"
)

// sends a random resolved address to channel, refreshing DNS every
// once in a while
func dns(ch chan string, ttl time.Duration, hosts ...string) {
	rand.Seed(time.Now().UnixNano())

	var addrs []string
	next_update_time := time.Unix(0, 0)

	for {
		now := time.Now()

		if now.After(next_update_time) {
			addrs = make([]string, 0)

			for _, h := range hosts {
				// log.Println("resolving", h)

				haddrs, err := net.LookupHost(h)

				if err != nil {
					log.Fatal(err)
				}

				addrs = append(addrs, haddrs...)
			}

			next_update_time = now.Add(ttl)
		}

		ch <- addrs[rand.Intn(len(addrs))]
	}
}
