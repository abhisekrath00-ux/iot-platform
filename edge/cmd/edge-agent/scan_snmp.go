package main

import (
	"context"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Remote probing is a separate default-off permission from local discovery.
// Only the configured variable may be read. Secrets and raw probe errors never
// enter the result. Local discovery findings are not reused or published here.
func requestedSNMP(ctx context.Context, r scanRequest, c config.Autodetect, lookup func(string) (string, bool), scan func(context.Context, string, string, time.Duration) ([]discover.SNMPHit, error)) ([]discover.SNMPHit, string, error) {
	fail := func(s string) ([]discover.SNMPHit, string, error) { return nil, "", fmt.Errorf("%s", s) }
	if !c.SNMP || !c.SNMPDashboard || (c.Enabled != nil && !*c.Enabled) {
		return fail("dashboard SNMP scans are disabled on this gateway")
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(r.CommunityEnv) || r.CommunityEnv != c.SNMPCommunityEnv {
		return fail("SNMP environment name must match gateway configuration")
	}
	p, err := netip.ParsePrefix(r.CIDR)
	if err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p.Bits() < 24 {
		return fail("SNMP needs private IPv4 /24 or smaller")
	}
	secret, ok := lookup(r.CommunityEnv)
	if !ok || strings.TrimSpace(secret) == "" {
		return fail("configured SNMP environment variable is empty or unset")
	}
	hits, err := scan(ctx, p.Masked().String(), secret, 800*time.Millisecond)
	out := []discover.SNMPHit{}
	seen := map[string]bool{}
	for _, h := range hits {
		a, e := netip.ParseAddr(h.Addr)
		if e != nil || !p.Contains(a) || seen[h.Addr] {
			continue
		}
		seen[h.Addr] = true
		out = append(out, h)
	}
	note := "SNMPv2c sysName/sysDescr only; community travels in clear text. Findings are proposals, not enrolled devices. No reply does not prove absence."
	if err != nil {
		note += " Incomplete scan; only answered hosts shown."
	}
	return out, note, nil
}
