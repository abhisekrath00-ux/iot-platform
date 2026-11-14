package explorer

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

type temporaryDriver struct {
	fakeReader
	closed atomic.Int32
}

func (t *temporaryDriver) Poll(context.Context) ([]driver.Reading, error) {
	panic("explorer must not poll")
}
func (t *temporaryDriver) Close() error { t.closed.Add(1); return nil }
func temporaryFixture(t *testing.T) (*Registry, *temporaryDriver, *atomic.Int32) {
	t.Helper()
	d := &temporaryDriver{}
	calls := &atomic.Int32{}
	r := &Registry{factory: func(c config.Device) (driver.Driver, error) {
		calls.Add(1)
		if len(c.Points) != 0 || len(c.Writes) != 0 {
			t.Error("temporary has configured effects")
		}
		return d, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	r.EnableTemporary(ctx)
	t.Cleanup(func() { cancel(); r.closeTemporary() })
	return r, d, calls
}
func tcpChoice() Connect { return Connect{Transport: "tcp", Host: "127.0.0.1", NetPort: 1502, Unit: 7} }
func TestTemporaryExplicitNoReadSingleAndClosedIDs(t *testing.T) {
	r, d, calls := temporaryFixture(t)
	if len(r.Connections()) != 0 || calls.Load() != 0 {
		t.Fatal("page/list opened device")
	}
	info, err := r.OpenTemporary(context.Background(), tcpChoice())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Temporary || info.ID == "" || info.ExpiresAt.IsZero() || d.calls.Load() != 0 || calls.Load() != 1 {
		t.Fatal("open sent read or invalid lifetime", info)
	}
	if _, err := r.OpenTemporary(context.Background(), tcpChoice()); err == nil || calls.Load() != 1 {
		t.Fatal("second connection opened")
	}
	if _, err := r.Read(context.Background(), Range{info.ID, 3, 0, 1}); err != nil || d.calls.Load() != 1 {
		t.Fatal(err)
	}
	if err := r.DisconnectTemporary(info.ID); err != nil {
		t.Fatal(err)
	}
	if d.closed.Load() != 1 {
		t.Fatal("not closed exactly once")
	}
	if _, err := r.Read(context.Background(), Range{info.ID, 3, 0, 1}); err == nil || d.calls.Load() != 1 {
		t.Fatal("stale id read")
	}
	if err := r.DisconnectTemporary(info.ID); err == nil || d.closed.Load() != 1 {
		t.Fatal("stale close touched another device")
	}
	next, err := r.OpenTemporary(context.Background(), tcpChoice())
	if err != nil || next.ID == info.ID {
		t.Fatal("reused lifetime", err)
	}
}
func TestTemporaryIdleHardExpiryAndShutdown(t *testing.T) {
	for _, kind := range []string{"idle", "hard", "read expiry", "shutdown"} {
		t.Run(kind, func(t *testing.T) {
			r, d, _ := temporaryFixture(t)
			info, err := r.OpenTemporary(context.Background(), tcpChoice())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			r.mu.Lock()
			e := r.entries[info.ID]
			switch kind {
			case "idle", "read expiry":
				e.temp.last = now.Add(-11 * time.Minute)
			case "hard":
				e.temp.created = now.Add(-31 * time.Minute)
			}
			r.mu.Unlock()
			switch kind {
			case "read expiry":
				if _, err := r.Read(context.Background(), Range{info.ID, 3, 0, 1}); err == nil {
					t.Fatal("expired read allowed")
				}
			case "shutdown":
				r.closeTemporary()
			default:
				r.expireTemporary(now)
			}
			if len(r.Connections()) != 0 || d.closed.Load() != 1 || d.calls.Load() != 0 {
				t.Fatal("expired handle retained or read")
			}
			if kind == "shutdown" {
				if _, err := r.OpenTemporary(context.Background(), tcpChoice()); err == nil {
					t.Fatal("reopened after shutdown")
				}
			}
		})
	}
}
func TestTemporaryBusyReadNeverQueuesDisconnectOrOpen(t *testing.T) {
	r, d, calls := temporaryFixture(t)
	info, err := r.OpenTemporary(context.Background(), tcpChoice())
	if err != nil {
		t.Fatal(err)
	}
	d.entered = make(chan struct{})
	d.release = make(chan struct{})
	done := make(chan struct{})
	go func() { r.Read(context.Background(), Range{info.ID, 3, 0, 1}); close(done) }()
	<-d.entered
	if err := r.DisconnectTemporary(info.ID); err == nil {
		t.Fatal("closed during read")
	}
	if _, err := r.OpenTemporary(context.Background(), tcpChoice()); err == nil || calls.Load() != 1 {
		t.Fatal("queued new connection")
	}
	if d.closed.Load() != 0 {
		t.Fatal("closed before drain")
	}
	close(d.release)
	<-done
	if err := r.DisconnectTemporary(info.ID); err != nil || d.closed.Load() != 1 {
		t.Fatal("explicit close after read failed", err)
	}
}
func TestTemporaryValidationBeforeOpenAndExistingOwner(t *testing.T) {
	r, _, calls := temporaryFixture(t)
	invalid := []Connect{{Transport: "tcp", Host: "255.255.255.255", NetPort: 502, Unit: 1}, {}, {Transport: "tcp", Host: "meter.local", NetPort: 502, Unit: 1}, {Transport: "tcp", Host: "0.0.0.0", NetPort: 502, Unit: 1}, {Transport: "tcp", Host: "224.0.0.1", NetPort: 502, Unit: 1}, {Transport: "tcp", Host: "127.0.0.1", NetPort: 502, Port: "COM3", Unit: 1}, {Transport: "tcp", Host: "::1", NetPort: 502, Unit: 0}, {Transport: "rtu", Port: "COM3", Baud: 9600, DataBits: 8, StopBits: 1, Parity: "bad", Unit: 1}, {Transport: "rtu", Port: "COM3\n", Baud: 9600, DataBits: 8, StopBits: 1, Parity: "none", Unit: 1}}
	for _, c := range invalid {
		if _, err := r.OpenTemporary(context.Background(), c); err == nil {
			t.Fatal("invalid accepted", c)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid choice opened device")
	}
	remove := r.Add(Connection{Transport: "tcp", Endpoint: "127.0.0.1:1502"}, &fakeReader{})
	defer remove()
	if _, err := r.OpenTemporary(context.Background(), tcpChoice()); err == nil || calls.Load() != 0 {
		t.Fatal("existing endpoint reopened")
	}
	borrowed := r.Connections()[0]
	if err := r.DisconnectTemporary(borrowed.ID); err == nil {
		t.Fatal("closed borrowed driver")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.OpenTemporary(ctx, Connect{Transport: "tcp", Host: "::1", NetPort: 502, Unit: 1}); err == nil || calls.Load() != 0 {
		t.Fatal("cancelled open ran")
	}
}

const connectJSON = `{"transport":"tcp","host":"127.0.0.1","net_port":1502,"unit":7}`

func TestTemporaryAPIGuardsBeforeOpen(t *testing.T) {
	r, d, calls := temporaryFixture(t)
	s, err := New("127.0.0.1:19191", r)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body string
		mutate                   string
		code                     int
	}{
		{"GET", "GET", "/api/connect", connectJSON, "", 405}, {"origin", "POST", "/api/connect", connectJSON, "origin", 403}, {"token", "POST", "/api/connect", connectJSON, "token", 403}, {"host", "POST", "/api/connect", connectJSON, "host", 403}, {"peer", "POST", "/api/connect", connectJSON, "peer", 403}, {"write field", "POST", "/api/connect", `{"transport":"tcp","host":"127.0.0.1","net_port":502,"unit":7,"write":true}`, "", 400}, {"second object", "POST", "/api/connect", connectJSON + connectJSON, "", 400}, {"close extra", "POST", "/api/disconnect", `{"id":"a","port":"COM3"}`, "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(s, tc.method, tc.path, tc.body)
			switch tc.mutate {
			case "origin":
				req.Header.Set("Origin", "http://foreign.invalid")
			case "token":
				req.Header.Del("X-Explorer-Session")
			case "host":
				req.Host = "foreign.invalid"
			case "peer":
				req.RemoteAddr = "192.168.1.2:12"
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			if w.Code != tc.code || calls.Load() != 0 {
				t.Fatal(w.Code, w.Body.String(), calls.Load())
			}
		})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request(s, "POST", "/api/connect", connectJSON))
	var info Connection
	json.Unmarshal(w.Body.Bytes(), &info)
	if w.Code != 200 || info.ID == "" || calls.Load() != 1 || d.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	b, _ := json.Marshal(map[string]string{"id": info.ID})
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request(s, "POST", "/api/disconnect", string(b)))
	if w.Code != 204 || d.closed.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
