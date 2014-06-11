package main

import (
	czdns "github.com/cznic/dns"
	"github.com/cznic/dns/resolver"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"syscall"
	"time"
)

// Custom DNS resolver function.
//
// 'dnsServer' DNS server to use
// 'host' Host that we're trying to resolve

func customResolve(dnsServer string, host string) (addrs []string, err error) {
	tmpFile := func(content string) (name string, err error) {
		f, err := ioutil.TempFile("", "gb")

		if err != nil {
			return
		}

		err = ioutil.WriteFile(f.Name(), []byte(content), (os.FileMode)(0644))

		if err != nil {
			return
		}

		name = f.Name()

		return
	}

	// fake /etc/hosts
	hostsFile, err := tmpFile("127.0.0.1	localhost\n")
	if err != nil {
		return
	}
	defer syscall.Unlink(hostsFile)

	// fake /etc/resolv.conf
	resolvConfFile, err := tmpFile("nameserver " + dnsServer + "\n")
	if err != nil {
		return
	}
	defer syscall.Unlink(resolvConfFile)

	l := czdns.NewLogger(nil, czdns.LOG_ERRORS)
	r, err := resolver.New(hostsFile, resolvConfFile, l)
	if err != nil {
		return
	}

	ipAddrs, _, err := r.GetHostByNameIPv4(host)
	if err != nil {
		return
	}

	addrs = make([]string, len(ipAddrs), len(ipAddrs))
	for i, ip := range ipAddrs {
		addrs[i] = ip.String()
	}

	return
}

// Sends a random resolved address to channel, refreshing DNS every
// once in a while.
//
// If 'dnsServer' is empty, system resolver will be used.
func dns(dnsServer string, ch chan string, ttl time.Duration, hosts ...string) {
	rand.Seed(time.Now().UnixNano())

	var addrs []string
	next_update_time := time.Unix(0, 0)

	for {
		now := time.Now()

		if now.After(next_update_time) {
			addrs = make([]string, 0)

			for _, h := range hosts {
				var haddrs []string
				var err error

				// log.Println("resolving", h)
				if dnsServer != "" {
					haddrs, err = customResolve(dnsServer, h)
				} else {
					haddrs, err = net.LookupHost(h)
				}

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
