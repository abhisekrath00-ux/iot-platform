package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"strings"
	"testing"
	"time"
)

func TestRequestedSNMPGates(t *testing.T) {
	good := config.Autodetect{SNMP: true, SNMPDashboard: true, SNMPCommunityEnv: "PLANT_SCAN"}
	r := scanRequest{CommunityEnv: "PLANT_SCAN", CIDR: "192.168.1.9/24"}
	calls := 0
	lookup := func(k string) (string, bool) {
		calls++
		if k != "PLANT_SCAN" {
			t.Fatal(k)
		}
		return "secret-not-for-result", true
	}
	scan := func(ctx context.Context, cidr, secret string, to time.Duration) ([]discover.SNMPHit, error) {
		calls++
		if cidr != "192.168.1.0/24" || secret != "secret-not-for-result" || to != 800*time.Millisecond {
			t.Fatal("bad scan parameters")
		}
		return []discover.SNMPHit{{Addr: "192.168.1.9", Name: "simulated"}, {Addr: "192.168.1.9"}, {Addr: "8.8.8.8"}}, errors.New("secret-not-for-result")
	}
	for _, c := range []config.Autodetect{{}, {SNMP: true, SNMPCommunityEnv: "PLANT_SCAN"}, {SNMPDashboard: true, SNMPCommunityEnv: "PLANT_SCAN"}} {
		if _, _, e := requestedSNMP(t.Context(), r, c, lookup, scan); e == nil {
			t.Fatal("gate bypass")
		}
	}
	for _, bad := range []scanRequest{{CommunityEnv: "OTHER", CIDR: r.CIDR}, {CommunityEnv: "x;cat", CIDR: r.CIDR}, {CommunityEnv: r.CommunityEnv, CIDR: "8.8.8.0/24"}, {CommunityEnv: r.CommunityEnv, CIDR: "10.0.0.0/23"}} {
		if _, _, e := requestedSNMP(t.Context(), bad, good, lookup, scan); e == nil {
			t.Fatal("request bypass")
		}
	}
	if calls != 0 {
		t.Fatal("looked up secret or scanned before validation")
	}
	if _, _, e := requestedSNMP(t.Context(), r, good, func(string) (string, bool) { return " ", true }, scan); e == nil {
		t.Fatal("blank secret")
	}
	out, note, e := requestedSNMP(t.Context(), r, good, lookup, scan)
	if e != nil || len(out) != 1 || !strings.Contains(note, "Incomplete") || strings.Contains(note, "secret-not-for-result") {
		t.Fatalf("%+v %s %v", out, note, e)
	}
	off := false
	good.Enabled = &off
	if _, _, e := requestedSNMP(t.Context(), r, good, lookup, scan); e == nil {
		t.Fatal("global disable ignored")
	}
}
func TestScanRequestSNMPDefaultOff(t *testing.T) {
	r := runScanRequest(t.Context(), []byte(`{"scan_id":"s","kind":"snmp","cidr":"192.168.1.0/24","community_env":"PLANT_SCAN"}`), nil)
	b, _ := json.Marshal(r)
	if r.OK || !strings.Contains(r.Error, "disabled") || strings.Contains(string(b), "PLANT_SCAN") {
		t.Fatalf("%s", b)
	}
}
