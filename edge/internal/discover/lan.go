package discover

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Ports probed by ScanLAN: plain TCP connects only, no protocol payload is sent.
var Ports = map[int]string{502: "Modbus TCP", 4840: "OPC UA", 2404: "IEC 60870-5-104", 20000: "DNP3", 102: "IEC 61850 MMS / S7", 1883: "MQTT", 8883: "MQTT TLS"}

const maxHosts = 1024

// Hit is one open port on one host.
type Hit struct {
	Addr    string
	Port    int
	Service string
}

// Hosts expands a private IPv4 CIDR (at most a /22). Public ranges are refused so the
// scanner cannot be pointed at third parties from a customer's network.
func Hosts(cidr string) ([]string, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return nil, fmt.Errorf("need an IPv4 CIDR like 192.168.1.0/24")
	}
	if !p.Addr().IsPrivate() {
		return nil, fmt.Errorf("only private (RFC 1918) ranges may be scanned")
	}
	if p.Bits() < 22 {
		return nil, fmt.Errorf("range too large: use /22 or smaller (max %d hosts)", maxHosts)
	}
	var out []string
	for a := p.Masked().Addr(); p.Contains(a); a = a.Next() {
		out = append(out, a.String())
	}
	if len(out) > 2 {
		out = out[1 : len(out)-1] // drop network and broadcast
	}
	return out, nil
}

// ScanLAN TCP-connects to the known industrial ports on each host. It is a
// discovery aid: an open port means something is listening, not that it is a
// compatible device. Rate is bounded by a fixed worker count.
func ScanLAN(ctx context.Context, cidr string, timeout time.Duration) ([]Hit, error) {
	hosts, err := Hosts(cidr)
	if err != nil {
		return nil, err
	}
	type job struct {
		h string
		p int
	}
	jobs := make(chan job)
	var mu sync.Mutex
	var hits []Hit
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				d := net.Dialer{Timeout: timeout}
				c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(j.h, fmt.Sprint(j.p)))
				if err == nil {
					c.Close()
					mu.Lock()
					hits = append(hits, Hit{j.h, j.p, Ports[j.p]})
					mu.Unlock()
				}
			}
		}()
	}
loop:
	for _, h := range hosts {
		for p := range Ports {
			select {
			case jobs <- job{h, p}:
			case <-ctx.Done():
				break loop
			}
		}
	}
	close(jobs)
	wg.Wait()
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Addr != hits[j].Addr {
			a, _ := netip.ParseAddr(hits[i].Addr)
			b, _ := netip.ParseAddr(hits[j].Addr)
			return a.Less(b)
		}
		return hits[i].Port < hits[j].Port
	})
	return hits, nil
}
