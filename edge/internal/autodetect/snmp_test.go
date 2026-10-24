package autodetect

import (
	"context"
	"errors"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSNMPOptInLocalOnly(t *testing.T) {
	off := false
	cfg := &config.Config{Autodetect: config.Autodetect{Serial: &off, LAN: &off, BACnet: &off, SNMPCommunityEnv: "PLANT_SCAN"}}
	d, _ := fakeDetector(cfg)
	calls := 0
	d.LookupEnv = func(k string) (string, bool) {
		if k != "PLANT_SCAN" {
			t.Fatal(k)
		}
		return "secret-not-for-output", true
	}
	d.ScanSNMP = func(ctx context.Context, cidr, community string, to time.Duration) ([]discover.SNMPHit, error) {
		calls++
		if cidr != "192.168.1.0/24" || community != "secret-not-for-output" || to != 800*time.Millisecond {
			t.Fatal("probe params")
		}
		return []discover.SNMPHit{{Addr: "192.168.1.9", Name: "test name", Descr: "simulated switch"}, {Addr: "8.8.8.8"}, {Addr: "192.168.1.9"}}, nil
	}
	if r := d.Run(context.Background()); calls != 0 || len(r.Findings) != 0 {
		t.Fatal("default not off")
	}
	cfg.Autodetect.SNMP = true
	r := d.Run(context.Background())
	if calls != 1 || len(r.Findings) != 1 || r.Findings[0].SNMPName != "test name" || r.Findings[0].Confident {
		t.Fatalf("%+v", r)
	}
	st := OpenStore(filepath.Join(t.TempDir(), "discoveries.json"))
	st.Merge(fixed(), r.Findings, r.Notes)
	st.MarkState(r.Findings[0].Key, "ignored")
	st.Merge(fixed(), r.Findings, r.Notes)
	s := st.Snapshot()
	if s.Findings[0].State != "ignored" {
		t.Fatal("ignore lost")
	}
	if len(Payloads(r.Findings, 1, 32, fixed())) != 0 {
		t.Fatal("SNMP leaked to cloud payload")
	}
	if ds, e := AutoAdd(cfg, Builtin(), r.Findings, filepath.Join(t.TempDir(), "overlay")); e != nil || len(ds) != 0 {
		t.Fatal("auto-added SNMP")
	}
	d.LookupEnv = func(string) (string, bool) { return "", false }
	r = d.Run(context.Background())
	if calls != 1 || len(r.Findings) != 0 || len(r.Notes) == 0 {
		t.Fatal("unset secret not skipped")
	}
	d.LookupEnv = func(string) (string, bool) { return "secret-not-for-output", true }
	d.ScanSNMP = func(context.Context, string, string, time.Duration) ([]discover.SNMPHit, error) {
		return nil, errors.New("secret-not-for-output")
	}
	r = d.Run(context.Background())
	if strings.Contains(strings.Join(r.Notes, " "), "secret-not-for-output") {
		t.Fatal("secret error leak")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = d.Run(ctx)
	if len(r.Findings) != 0 || len(r.Notes) == 0 {
		t.Fatal("cancel")
	}
}
func TestSNMPRangeBounds(t *testing.T) {
	off := false
	cfg := &config.Config{Autodetect: config.Autodetect{Serial: &off, LAN: &off, BACnet: &off, SNMP: true, SNMPCommunityEnv: "SCAN"}}
	d, _ := fakeDetector(cfg)
	d.LookupEnv = func(string) (string, bool) { return "test", true }
	calls := 0
	d.ScanSNMP = func(context.Context, string, string, time.Duration) ([]discover.SNMPHit, error) {
		calls++
		return nil, nil
	}
	d.Nets = func() []Net {
		return []Net{{CIDR: "8.8.8.0/24"}, {CIDR: "192.168.0.0/22"}, {CIDR: "192.168.1.0/24"}, {CIDR: "192.168.1.0/24"}}
	}
	r := d.Run(context.Background())
	if calls != 1 || len(r.Notes) != 2 {
		t.Fatalf("%d %+v", calls, r)
	}
}

func TestSNMPPartialCancellationAndSubnetCap(t *testing.T) {
	off := false
	cfg := &config.Config{Autodetect: config.Autodetect{Serial: &off, LAN: &off, BACnet: &off, SNMP: true, SNMPCommunityEnv: "SCAN"}}
	d, _ := fakeDetector(cfg)
	d.LookupEnv = func(string) (string, bool) { return "sim-only", true }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ScanSNMP = func(context.Context, string, string, time.Duration) ([]discover.SNMPHit, error) {
		cancel()
		return []discover.SNMPHit{{Addr: "192.168.1.9", Name: "partial"}}, context.Canceled
	}
	r := d.Run(ctx)
	if len(r.Findings) != 1 || len(r.Notes) == 0 {
		t.Fatal(r)
	}
	calls := 0
	d.ScanSNMP = func(context.Context, string, string, time.Duration) ([]discover.SNMPHit, error) {
		calls++
		return nil, nil
	}
	d.Nets = func() []Net {
		n := []Net{}
		for i := 0; i < 20; i++ {
			n = append(n, Net{CIDR: fmt.Sprintf("192.168.%d.0/24", i)})
		}
		return n
	}
	r = d.Run(context.Background())
	if calls != 16 || len(r.Notes) != 1 {
		t.Fatalf("%d %+v", calls, r)
	}
}
