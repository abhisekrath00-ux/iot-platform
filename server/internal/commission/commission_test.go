package commission

import (
	"strings"
	"testing"
)

func goodProbe() Probe {
	return Probe{Port: "/dev/ttyUSB0", Baud: 9600, DataBits: 8, StopBits: 1,
		Parity: "none", Address: 1, Func: 3, Register: 0, Count: 2, Type: "f32", WordOrder: "abcd"}
}

func TestValidateProbe(t *testing.T) {
	if err := ValidateProbe(goodProbe()); err != nil {
		t.Fatalf("good probe rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Probe)
	}{
		{"relative path", func(p *Probe) { p.Port = "ttyUSB0" }},
		{"path escape", func(p *Probe) { p.Port = "/dev/../etc/passwd" }},
		{"non-tty device", func(p *Probe) { p.Port = "/dev/sda" }},
		{"weird baud", func(p *Probe) { p.Baud = 12345 }},
		{"broadcast address", func(p *Probe) { p.Address = 0 }},
		{"address over max", func(p *Probe) { p.Address = 248 }},
		{"write function 6", func(p *Probe) { p.Func = 6 }},
		{"write function 16", func(p *Probe) { p.Func = 16 }},
		{"zero count", func(p *Probe) { p.Count = 0 }},
		{"count overflow", func(p *Probe) { p.Register = 65535; p.Count = 2 }},
		{"bad type", func(p *Probe) { p.Type = "u24" }},
		{"bad word order", func(p *Probe) { p.WordOrder = "zzzz" }},
		{"huge timeout", func(p *Probe) { p.TimeoutMs = 60000 }},
		{"bad parity", func(p *Probe) { p.Parity = "space" }},
	}
	for _, c := range cases {
		p := goodProbe()
		c.mut(&p)
		if err := ValidateProbe(p); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestQRPayload(t *testing.T) {
	got := QRPayload("https://api.example.com", "ABCD-EFGH", "AXON-0001")
	if !strings.HasPrefix(got, "hexmon://claim?") {
		t.Fatalf("scheme: %s", got)
	}
	for _, want := range []string{"api=https%3A%2F%2Fapi.example.com", "serial=AXON-0001", "code=ABCD-EFGH"} {
		if !strings.Contains(got, want) {
			t.Errorf("payload %s missing %s", got, want)
		}
	}
}

func TestDeriveState(t *testing.T) {
	ok, bad := true, false
	cases := []struct {
		f    Facts
		want string
	}{
		{Facts{}, "awaiting_claim"},
		{Facts{Claimed: true}, "claimed"},
		{Facts{Claimed: true, HasDevice: true}, "profiled"},
		{Facts{Claimed: true, HasDevice: true, TestOK: &ok}, "tested"},
		{Facts{Claimed: true, HasDevice: true, TestOK: &bad}, "failed"},
		{Facts{Claimed: true, HasDevice: true, TestOK: &ok, Live: true}, "live"},
		{Facts{Claimed: true, Live: true}, "live"}, // skipped steps still end live
	}
	for _, c := range cases {
		if got := DeriveState(c.f); got != c.want {
			t.Errorf("%+v: got %s want %s", c.f, got, c.want)
		}
	}
}
