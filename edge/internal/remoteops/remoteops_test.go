package remoteops

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func req(id, kind string, mut func(*Request)) []byte {
	now := time.Now()
	q := Request{OpID: id, Kind: kind, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(5 * time.Minute)}
	if mut != nil {
		mut(&q)
	}
	b, _ := json.Marshal(q)
	return b
}

func TestLogsTailRedactedAndCapped(t *testing.T) {
	r := &Ring{}
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(r, "line %d\n", i)
	}
	fmt.Fprintf(r, "mqtt connect password=hunter2 ok\n")
	fmt.Fprintf(r, "auth Authorization: Bearer abc.def.ghi sent\n")
	fmt.Fprintf(r, "partial no newline")
	h := NewHandler(r, false)
	res, ok, restart := h.Handle(req("op-logs-0001", "logs", func(q *Request) { q.Lines = 3 }), time.Now())
	if !ok || restart || !res.OK || len(res.Lines) != 3 {
		t.Fatalf("%+v ok=%v restart=%v", res, ok, restart)
	}
	all := strings.Join(res.Lines, "\n")
	if strings.Contains(all, "hunter2") || strings.Contains(all, "abc.def") {
		t.Fatalf("secret leaked: %s", all)
	}
	if !strings.Contains(all, "password=[redacted]") {
		t.Fatalf("expected redaction marker: %s", all)
	}
	// ring bounded, asks above the cap are clipped
	res, _, _ = h.Handle(req("op-logs-0002", "logs", func(q *Request) { q.Lines = 100000 }), time.Now())
	if len(res.Lines) != MaxLines {
		t.Fatalf("lines=%d", len(res.Lines))
	}
	if len(r.lines) > ringLines {
		t.Fatalf("ring grew: %d", len(r.lines))
	}
}

func TestGateRefusals(t *testing.T) {
	h := NewHandler(&Ring{}, true)
	now := time.Now()
	good := req("op-restart-01", "restart", func(q *Request) { q.ApprovedBy = "u2" })
	cases := map[string][]byte{
		"expired":     req("op-exp-00001", "logs", func(q *Request) { q.ExpiresAt = now.Add(-time.Second); q.IssuedAt = now.Add(-time.Minute) }),
		"future":      req("op-fut-00001", "logs", func(q *Request) { q.IssuedAt = now.Add(time.Hour); q.ExpiresAt = now.Add(time.Hour + time.Minute) }),
		"long ttl":    req("op-ttl-00001", "logs", func(q *Request) { q.ExpiresAt = now.Add(time.Hour) }),
		"unknown":     req("op-unk-00001", "reboot", nil),
		"bad id":      req("x", "logs", nil),
		"unknown key": []byte(`{"op_id":"op-key-00001","kind":"logs","issued_at":"` + now.Format(time.RFC3339) + `","expires_at":"` + now.Add(time.Minute).Format(time.RFC3339) + `","shell":"rm -rf /"}`),
		"not json":    []byte(`nope`),
		"no approver": req("op-noap-0001", "restart", nil),
	}
	for name, p := range cases {
		res, _, restart := h.Handle(p, now)
		if res.OK || restart {
			t.Errorf("%s accepted: %+v", name, res)
		}
	}
	// replay: first accepted, second refused
	if res, _, restart := h.Handle(good, now); !res.OK || !restart {
		t.Fatalf("good restart: %+v", res)
	}
	if res, _, restart := h.Handle(good, now); res.OK || restart || !strings.Contains(res.Detail, "already seen") {
		t.Fatalf("replay: %+v", res)
	}
}

func TestRestartNeedsLocalOptIn(t *testing.T) {
	h := NewHandler(&Ring{}, false)
	res, ok, restart := h.Handle(req("op-rs-000001", "restart", func(q *Request) { q.ApprovedBy = "u2" }), time.Now())
	if res.OK || restart || !ok || !strings.Contains(res.Detail, "remote_restart") {
		t.Fatalf("%+v", res)
	}
}
