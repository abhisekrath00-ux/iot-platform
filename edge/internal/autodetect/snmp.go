package autodetect

import (
	"context"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Local-only opt-in proposals. Never guess a community, include it in notes,
// publish it to the server, or use these findings for AutoAdd.
func (d *Detector) snmpPass(ctx context.Context, nets []Net) ([]Finding, []string) {
	env := d.Cfg.Autodetect.SNMPCommunityEnv
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(env) {
		return nil, []string{"snmp: skipped; configure an environment variable name"}
	}
	community, ok := d.LookupEnv(env)
	if !ok || strings.TrimSpace(community) == "" {
		return nil, []string{"snmp: skipped; configured community environment variable is empty or unset"}
	}
	if len(nets) == 0 {
		return nil, []string{"snmp: no private IPv4 subnet found"}
	}
	out := []Finding{}
	notes := []string{}
	seen := map[string]bool{}
	ranges := map[string]bool{}
	self := localAddrs()
	now := d.Now().UTC()
	for _, n := range nets {
		if ctx.Err() != nil {
			notes = append(notes, "snmp: stopped early; context ended")
			break
		}
		p, err := netip.ParsePrefix(n.CIDR)
		if err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p.Bits() < 24 {
			notes = append(notes, "snmp: skipped invalid range (private IPv4 /24 or smaller required)")
			continue
		}
		cidr := p.Masked().String()
		if ranges[cidr] {
			continue
		}
		if len(ranges) >= 16 {
			notes = append(notes, "snmp: stopped at 16 subnet cap")
			break
		}
		ranges[cidr] = true
		hits, err := d.ScanSNMP(ctx, cidr, community, 800*time.Millisecond)
		// Keep any answered hosts from a cancelled pass, but do not label it complete.
		for _, h := range hits {
			addr, e := netip.ParseAddr(h.Addr)
			if e != nil || !p.Contains(addr) || self[h.Addr] || seen[h.Addr] {
				continue
			}
			seen[h.Addr] = true
			out = append(out, Finding{Key: "snmp:" + h.Addr, Kind: "snmp", Host: h.Addr, NetPort: 161, SNMPName: h.Name, SNMPDescr: h.Descr, FirstSeen: now, LastSeen: now})
		}
		if err != nil {
			notes = append(notes, "snmp: scan incomplete; one or more subnets could not finish")
		}
	}
	return out, notes
}
