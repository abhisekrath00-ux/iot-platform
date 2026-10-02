package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/autodetect"
)

func TestDiscoveriesCLIEmptyAndFilled(t *testing.T) {
	t.Setenv("HEXMON_DATA_DIR", t.TempDir())
	var b bytes.Buffer
	if discoveriesCLI(&b) != 0 || !strings.Contains(b.String(), "no autodetect pass has run yet") {
		t.Fatalf("empty: %q", b.String())
	}
	b.Reset()
	s := autodetect.OpenStore(discoveriesPath())
	s.Merge(time.Now(), []autodetect.Finding{
		{Key: "k1", Kind: "modbus-rtu", Port: "COM3", Baud: 9600, Parity: "none", Address: 2, Confident: true},
		{Key: "k2", Kind: "lan", Host: "10.0.0.5", NetPort: 4840, Service: "OPC UA"},
	}, []string{"serial: nothing else"})
	if discoveriesCLI(&b) != 0 {
		t.Fatal("exit")
	}
	out := b.String()
	for _, want := range []string{"2 proposal(s)", "serial COM3 9600/none address 2", "[confident]", "network 10.0.0.5:4840", "note: serial: nothing else"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestIgnoreCLI(t *testing.T) {
	t.Setenv("HEXMON_DATA_DIR", t.TempDir())
	s := autodetect.OpenStore(discoveriesPath())
	s.Merge(time.Now(), []autodetect.Finding{{Key: "k1", Kind: "lan", Host: "10.0.0.5", NetPort: 502}}, nil)
	var b bytes.Buffer
	if ignoreCLI("nope", false, &b) != 1 {
		t.Fatal("unknown key should fail")
	}
	if ignoreCLI("k1", false, &b) != 0 || autodetect.OpenStore(discoveriesPath()).Snapshot().Findings[0].State != "ignored" {
		t.Fatal("not ignored")
	}
	if ignoreCLI("k1", true, &b) != 0 || autodetect.OpenStore(discoveriesPath()).Snapshot().Findings[0].State != "suggested" {
		t.Fatal("not restored")
	}
}
