package cfgpush

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodYAML = `devices:
  - id: meter-1
    profile: modbus-energy-meter
    port: /dev/ttyUSB0
    baud: 9600
    address: 3
    interval: 10s
    points:
      - {id: kw, register: 0, func: 4, type: u16, unit: kW, min: 0, max: 500}
`

type env struct {
	c    *Ctl
	priv ed25519.PrivateKey
	now  time.Time
}

func setup(t *testing.T, enabled bool) env {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return env{New(filepath.Join(t.TempDir(), "managed-devices.yaml"), pub, "ten", "SER1", enabled), priv, time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
}

func (e env) push(id string, ver int, y string, mut func(*Push)) []byte {
	h := sha256.Sum256([]byte(y))
	p := Push{PushID: id, TenantID: "ten", Serial: "SER1", Version: ver, DevicesYAML: y, SHA256: hex.EncodeToString(h[:]), IssuedAt: e.now, ExpiresAt: e.now.Add(time.Hour)}
	if mut != nil {
		mut(&p)
	}
	p.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(e.priv, Canonical(p)))
	b, _ := json.Marshal(p)
	return b
}

func TestApplyAndConfirm(t *testing.T) {
	e := setup(t, true)
	r := e.c.Apply(e.push("push-0001", 1, goodYAML, nil), e.now)
	if r.State != "applied" {
		t.Fatal(r)
	}
	b, _ := os.ReadFile(e.c.Path)
	if !strings.Contains(string(b), "meter-1") {
		t.Fatal("not written")
	}
	if res, _ := e.c.Settle(false, e.now.Add(time.Minute)); res != nil {
		t.Fatal("still within probation")
	}
	if res, _ := e.c.Settle(true, e.now.Add(time.Minute)); res == nil || res.State != "confirmed" {
		t.Fatal(res)
	}
	if res, _ := e.c.Settle(false, e.now.Add(time.Hour)); res != nil {
		t.Fatal("nothing left to settle")
	}
}

func TestRejections(t *testing.T) {
	e := setup(t, true)
	cases := map[string][]byte{
		"signature":  e.push("push-0002", 1, goodYAML, func(p *Push) { p.Version = 1; p.DevicesYAML = goodYAML + " " }),
		"tenant":     e.push("push-0003", 1, goodYAML, func(p *Push) { p.TenantID = "other" }),
		"gateway":    e.push("push-0004", 1, goodYAML, func(p *Push) { p.Serial = "SER2" }),
		"expired":    e.push("push-0005", 1, goodYAML, func(p *Push) { p.ExpiresAt = e.now.Add(-time.Second); p.IssuedAt = e.now.Add(-time.Hour) }),
		"lifetime":   e.push("push-0006", 1, goodYAML, func(p *Push) { p.ExpiresAt = e.now.Add(48 * time.Hour) }),
		"checksum":   e.push("push-0007", 1, goodYAML, func(p *Push) { p.SHA256 = strings.Repeat("0", 64) }),
		"unknown":    []byte(`{"push_id":"push-0008","extra":1}`),
		"parse":      e.push("push-0009", 1, "devices: [", nil),
		"field":      e.push("push-0010", 1, "devices:\n  - {id: a, bogus: 1, interval: 1s}\n", nil),
		"interval":   e.push("push-0011", 1, "devices:\n  - {id: a, profile: x}\n", nil),
		"duplicate":  e.push("push-0012", 1, "devices:\n  - {id: a, interval: 1s}\n  - {id: a, interval: 1s}\n", nil),
		"range":      e.push("push-0013", 1, "devices:\n  - {id: a, interval: 1s, points: [{id: p, min: 5, max: 5}]}\n", nil),
		"no-version": e.push("push-0014", 0, goodYAML, nil),
	}
	for name, payload := range cases {
		if r := e.c.Apply(payload, e.now); r.State != "rejected" {
			t.Errorf("%s accepted: %+v", name, r)
		}
	}
	if _, err := os.Stat(e.c.Path); err == nil {
		t.Error("a rejected push wrote a file")
	}
	// unsigned with no key configured
	e2 := setup(t, true)
	e2.c.Key = nil
	if r := e2.c.Apply(e2.push("push-0015", 1, goodYAML, nil), e2.now); r.State != "rejected" {
		t.Error("no key must refuse")
	}
	// not opted in
	e3 := setup(t, false)
	if r := e3.c.Apply(e3.push("push-0016", 1, goodYAML, nil), e3.now); r.State != "rejected" || !strings.Contains(r.Detail, "opted in") {
		t.Errorf("opt-in not enforced: %+v", r)
	}
}

func TestReplayVersionAndProbation(t *testing.T) {
	e := setup(t, true)
	if r := e.c.Apply(e.push("push-0020", 1, goodYAML, nil), e.now); r.State != "applied" {
		t.Fatal(r)
	}
	e.c.Settle(true, e.now)
	if r := e.c.Apply(e.push("push-0020", 2, goodYAML, nil), e.now); r.State != "rejected" {
		t.Error("push id reuse")
	}
	if r := e.c.Apply(e.push("push-0021", 1, goodYAML, nil), e.now); r.State != "rejected" {
		t.Error("same version")
	}
	y2 := strings.Replace(goodYAML, "meter-1", "meter-2", 1)
	if r := e.c.Apply(e.push("push-0022", 2, y2, nil), e.now); r.State != "applied" {
		t.Fatal(r)
	}
	if r := e.c.Apply(e.push("push-0023", 3, goodYAML, nil), e.now); r.State != "rejected" {
		t.Error("second push during probation")
	}
	// never healthy: roll back to version 1's list
	res, restart := e.c.Settle(false, e.now.Add(Probation+time.Second))
	if res == nil || res.State != "rolled_back" || !restart {
		t.Fatal(res)
	}
	b, _ := os.ReadFile(e.c.Path)
	if !strings.Contains(string(b), "meter-1") || strings.Contains(string(b), "meter-2") {
		t.Fatalf("not restored: %s", b)
	}
	if r := e.c.Apply(e.push("push-0024", 2, y2, nil), e.now.Add(time.Hour)); r.State != "rejected" {
		t.Error("failed version must not be re-applied")
	}
	if r := e.c.Apply(e.push("push-0025", 3, y2, func(p *Push) { p.IssuedAt = e.now.Add(time.Hour); p.ExpiresAt = e.now.Add(2 * time.Hour) }), e.now.Add(time.Hour)); r.State != "applied" {
		t.Errorf("a newer push works after rollback: %+v", r)
	}
}

func TestFirstPushRollbackRemovesFile(t *testing.T) {
	e := setup(t, true)
	e.c.Apply(e.push("push-0030", 1, goodYAML, nil), e.now)
	e.c.Settle(false, e.now.Add(Probation+time.Second))
	if _, err := os.Stat(e.c.Path); err == nil {
		t.Error("first push rollback should remove the managed file")
	}
}

func TestCanonicalVector(t *testing.T) {
	got := string(Canonical(Push{TenantID: "ten", Serial: "SER1", Version: 3, PushID: "push-0001", SHA256: "abc", ExpiresAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}))
	want := "hexmon-config-push-v1\nten\nSER1\n3\npush-0001\nabc\n2026-10-08T12:00:00Z"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestPreflightRestoresWhenFileBroken(t *testing.T) {
	e := setup(t, true)
	e.c.Apply(e.push("push-0040", 1, goodYAML, nil), e.now)
	e.c.Settle(true, e.now)
	y2 := strings.Replace(goodYAML, "meter-1", "meter-2", 1)
	e.c.Apply(e.push("push-0041", 2, y2, nil), e.now.Add(time.Hour))
	os.WriteFile(e.c.Path, []byte("devices: ["), 0o644) // corrupted after the push
	if res := e.c.Preflight(e.now.Add(time.Hour)); res == nil || res.State != "rolled_back" {
		t.Fatal(res)
	}
	b, _ := os.ReadFile(e.c.Path)
	if !strings.Contains(string(b), "meter-1") {
		t.Fatalf("not restored: %s", b)
	}
}
