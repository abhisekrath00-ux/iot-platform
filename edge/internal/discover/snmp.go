package discover

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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
	var mu sync.Mutex
	var out []SNMPHit
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, h := range hosts {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			defer func() { <-sem }()
			g := &gosnmp.GoSNMP{Target: h, Port: 161, Community: community, Version: gosnmp.Version2c, Timeout: timeout, Retries: 0}
			if g.Connect() != nil {
				return
			}
			defer g.Conn.Close()
			r, err := g.Get([]string{".1.3.6.1.2.1.1.1.0", ".1.3.6.1.2.1.1.5.0"})
			if err != nil || len(r.Variables) < 2 {
				return
			}
			str := func(v gosnmp.SnmpPDU) string {
				if b, ok := v.Value.([]byte); ok {
					return clip(string(b))
				}
				return ""
			}
			hit := SNMPHit{Addr: h, Descr: str(r.Variables[0]), Name: str(r.Variables[1])}
			if hit.Descr == "" && hit.Name == "" {
				return
			}
			mu.Lock()
			out = append(out, hit)
			mu.Unlock()
		}(h)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out, nil
}
