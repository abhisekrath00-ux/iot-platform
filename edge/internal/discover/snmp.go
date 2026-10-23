package discover

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
)

// SNMPHit is one host that answered an SNMPv2c GET for sysName/sysDescr.
type SNMPHit struct{ Addr, Name, Descr string }

func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
	if len(s) > 120 {
		s = s[:120]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// ScanSNMP sends one read-only GET (sysDescr.0, sysName.0) per host in a private
// range using the given v2c community. The community comes from the caller
// (an environment variable on the box); it is sent in clear text over UDP, as v2c always is.
func ScanSNMP(ctx context.Context, cidr, community string, timeout time.Duration) ([]SNMPHit, error) {
	hosts, err := Hosts(cidr)
	if err != nil {
		return nil, err
	}
	if community == "" {
		return nil, fmt.Errorf("community is empty")
	}
	if timeout < 50*time.Millisecond || timeout > 5*time.Second {
		return nil, fmt.Errorf("timeout must be 50ms-5s")
	}
	return scanSNMPHosts(ctx, hosts, community, 161, timeout)
}

// Kept separate so simulated agents can use an ephemeral loopback port;
// the public scanner still accepts only private ranges and UDP161.
func scanSNMPHosts(ctx context.Context, hosts []string, community string, port uint16, timeout time.Duration) ([]SNMPHit, error) {
	var mu sync.Mutex
	out := []SNMPHit{}
	var wg sync.WaitGroup
	jobs := make(chan string)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range jobs {
				if ctx.Err() != nil {
					return
				}
				hit, ok := probeSNMP(ctx, h, community, port, timeout)
				if ok {
					mu.Lock()
					out = append(out, hit)
					mu.Unlock()
				}
			}
		}()
	}
loop:
	for _, h := range hosts {
		select {
		case jobs <- h:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out, ctx.Err()
}

const sysDescrOID = ".1.3.6.1.2.1.1.1.0"
const sysNameOID = ".1.3.6.1.2.1.1.5.0"

func probeSNMP(ctx context.Context, host, community string, port uint16, timeout time.Duration) (SNMPHit, bool) {
	g := &gosnmp.GoSNMP{Target: host, Port: port, Community: community, Version: gosnmp.Version2c, Timeout: timeout, Retries: 0, Context: ctx}
	if g.Connect() != nil {
		return SNMPHit{}, false
	}
	defer g.Conn.Close()
	// Interrupt a blocked UDP read on cancellation, rather than waiting for
	// the configured timeout. Each worker owns its connection.
	stop := context.AfterFunc(ctx, func() { g.Conn.Close() })
	defer stop()
	r, err := g.Get([]string{sysDescrOID, sysNameOID})
	if err != nil || r == nil || r.Error != gosnmp.NoError {
		return SNMPHit{}, false
	}
	hit := SNMPHit{Addr: host}
	for _, v := range r.Variables {
		if v.Type != gosnmp.OctetString {
			continue
		}
		b, ok := v.Value.([]byte)
		if !ok {
			continue
		}
		value := clip(strings.ToValidUTF8(string(b), "?"))
		switch "." + strings.TrimPrefix(v.Name, ".") {
		case sysDescrOID:
			hit.Descr = value
		case sysNameOID:
			hit.Name = value
		}
	}
	return hit, hit.Descr != "" || hit.Name != ""
}
