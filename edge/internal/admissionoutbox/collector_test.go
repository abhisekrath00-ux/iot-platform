package admissionoutbox

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"

	edgeconfig "github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

type pollFunc func(context.Context) ([]driver.Reading, error)

func (f pollFunc) Poll(ctx context.Context) ([]driver.Reading, error) { return f(ctx) }
func sourceSamples() []driver.Reading {
	return []driver.Reading{{DeviceID: "d", PointID: "p", Value: 42, Unit: "V"}, {DeviceID: "d", PointID: "q", Value: 43, Unit: "A"}}
}
func collectorConfig(s *Session, r PollReader) CollectorConfig {
	return CollectorConfig{Enabled: true, Session: s, Reader: r, DeviceID: "d", Points: map[string]string{"p": "V", "q": "A"}}
}
func TestCollectorSamplePreflightAndConfig(t *testing.T) {
	s := sessionOpen(t, sessionConfig(t))
	for _, mode := range []string{"disabled", "session", "reader", "typednil", "device", "points", "point", "unit"} {
		t.Run(mode, func(t *testing.T) {
			cfg := collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) { t.Fatal("read during construction"); return nil, nil }))
			switch mode {
			case "disabled":
				cfg.Enabled = false
			case "session":
				cfg.Session = nil
			case "reader":
				cfg.Reader = nil
			case "typednil":
				var f pollFunc
				cfg.Reader = f
			case "device":
				cfg.DeviceID = "+"
			case "points":
				cfg.Points = nil
			case "point":
				cfg.Points = map[string]string{"+": "V"}
			case "unit":
				cfg.Points["p"] = string(make([]byte, 65))
			}
			if c, e := NewCollector(cfg); c != nil || e == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	for _, mode := range []string{"partial-error", "missing", "duplicate", "foreign-device", "foreign-point", "wrong-unit", "nan", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c, e := NewCollector(collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) {
				rows := sourceSamples()
				switch mode {
				case "partial-error":
					return rows[:1], errors.New("partial poll")
				case "missing":
					return rows[:1], nil
				case "duplicate":
					rows[1] = rows[0]
				case "foreign-device":
					rows[1].DeviceID = "other"
				case "foreign-point":
					rows[1].PointID = "other"
				case "wrong-unit":
					rows[1].Unit = "changed"
				case "nan":
					rows[1].Value = math.NaN()
				case "cancel":
					cancel()
				}
				return rows, nil
			})))
			if e != nil {
				t.Fatal(e)
			}
			if r, e := c.PollOnce(ctx); e == nil || r.Retained != 0 {
				t.Fatal(r, e)
			}
			status, _ := s.Status(t.Context())
			if status.Capacity.Retained != 0 || c.Status().Pending != 0 {
				t.Fatal(status, c.Status())
			}
		})
	}
}
func TestCollectorCapacityPendingSameIdentityAndStoppedRetention(t *testing.T) {
	cfg := sessionConfig(t)
	pin, key := trust(t)
	cfg.Trust = pin
	s := sessionOpen(t, cfg)
	calls := 0
	c, e := NewCollector(collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) { calls++; return sourceSamples(), nil })))
	if e != nil {
		t.Fatal(e)
	}
	result, e := c.PollOnce(t.Context())
	if !errors.Is(e, ErrCapacity) || result.Sampled != 2 || result.Retained != 1 || result.Pending != 1 || calls != 1 {
		t.Fatal(result, e, calls)
	}
	pending := c.pending[0]
	if result, e := c.PollOnce(t.Context()); !errors.Is(e, ErrCollectorPending) || result.Sampled != 0 || calls != 1 {
		t.Fatal(result, e, calls)
	}
	if _, e = c.RetainPending(t.Context()); !errors.Is(e, ErrCapacity) || c.pending[0] != pending || calls != 1 {
		t.Fatal(e, c.Status())
	}
	rows, e := s.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	first := rows[0]
	if first.Reading.ObservedAt != pending.ObservedAt || first.Reading.EventID == pending.EventID || first.Reading.ObservedAt.Nanosecond()%1000 != 0 {
		t.Fatal(first.Reading, pending)
	}
	if e = c.Stop(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = c.PollOnce(t.Context()); !errors.Is(e, ErrSessionClosed) {
		t.Fatal(e)
	}
	if ok, e := s.store.ReconcileSigned(t.Context(), first.Sequence, token(t, receipt(first), pin, key), pin); !ok || e != nil {
		t.Fatal(ok, e)
	}
	result, e = c.RetainPending(t.Context())
	if e != nil || result.Retained != 2 || result.Pending != 0 || calls != 1 {
		t.Fatal(result, e, calls)
	}
	rows, e = s.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 || rows[0].Reading != pending {
		t.Fatal(rows, e, pending)
	}
	if c.Status().State != "stopped" {
		t.Fatal(c.Status())
	}
}
func TestCollectorConcurrentAndStopDrain(t *testing.T) {
	cfg := sessionConfig(t)
	cfg.Store.Capacity = 2
	s := sessionOpen(t, cfg)
	entered := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	c, e := NewCollector(collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) { close(entered); <-finish; return sourceSamples(), nil })))
	if e != nil {
		t.Fatal(e)
	}
	go func() { _, e := c.PollOnce(t.Context()); done <- e }()
	<-entered
	if _, e = c.RetainPending(t.Context()); !errors.Is(e, ErrCollectorBusy) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if e = c.Stop(ctx); !errors.Is(e, context.DeadlineExceeded) || c.Status().State != "stopping" {
		t.Fatal(e, c.Status())
	}
	close(finish)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = c.Stop(t.Context()); e != nil || c.Status().State != "stopped" {
		t.Fatal(e, c.Status())
	}
	status, _ := s.Status(t.Context())
	if status.LastSend != nil || status.Capacity.Retained != 2 {
		t.Fatal(status)
	}
}

func TestCollectorActualTCPReadsToRetainedSession(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	var frames atomic.Int32
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		conn, e := ln.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		for {
			req := make([]byte, 12)
			if _, e = io.ReadFull(conn, req); e != nil {
				return
			}
			frames.Add(1)
			if req[7] != 3 && req[7] != 4 {
				t.Error("non-read function", req[7])
				return
			}
			count := binary.BigEndian.Uint16(req[10:])
			data := make([]byte, int(count)*2)
			for i := 0; i < int(count); i++ {
				binary.BigEndian.PutUint16(data[i*2:], uint16(42+i))
			}
			response := []byte{req[0], req[1], 0, 0, 0, byte(3 + len(data)), req[6], req[7], byte(len(data))}
			if _, e = conn.Write(append(response, data...)); e != nil {
				return
			}
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	src, e := driver.New(edgeconfig.Device{ID: "d", Profile: "modbus-tcp", Host: "127.0.0.1", NetPort: addr.Port, Address: 1, Points: []edgeconfig.Point{{ID: "p", Register: 0, Func: 3, Type: "u16", Unit: "V", Min: 0, Max: 100}, {ID: "q", Register: 1, Func: 4, Type: "u16", Unit: "A", Min: 0, Max: 100}}})
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	cfg := sessionConfig(t)
	cfg.Store.Capacity = 2
	s := sessionOpen(t, cfg)
	c, e := NewCollector(collectorConfig(s, src))
	if e != nil {
		t.Fatal(e)
	}
	result, e := c.PollOnce(t.Context())
	if e != nil || result.Retained != 2 || result.Pending != 0 || frames.Load() != 2 {
		t.Fatal(result, e, frames.Load())
	}
	status, e := s.Status(t.Context())
	if e != nil || !status.Capacity.Full || status.LastSend != nil {
		t.Fatal(status, e)
	}
	rows, e := s.store.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 || rows[0].Reading.Value != 42 || rows[1].Reading.Value != 42 {
		t.Fatal(rows, e)
	}
	if e = c.Stop(t.Context()); e != nil {
		t.Fatal(e)
	}
	src.Close()
	<-ended
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	reopened := sessionOpen(t, cfg)
	again, e := reopened.store.Next(t.Context(), 2)
	if e != nil || len(again) != 2 || again[0].InputSHA256 != rows[0].InputSHA256 || again[1].InputSHA256 != rows[1].InputSHA256 {
		t.Fatal(again, e)
	}
	t.Log("ACTUAL_READ_ONLY_TCP_COLLECTOR_SESSION_REOPEN_OK")
}

func TestCollectorAlreadyRetainedPendingAndClosedSession(t *testing.T) {
	cfg := sessionConfig(t)
	s := sessionOpen(t, cfg)
	calls := 0
	c, e := NewCollector(collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) { calls++; return sourceSamples(), nil })))
	if e != nil {
		t.Fatal(e)
	}
	original := reading()
	original.DeviceID = "d"
	original.PointID = "p"
	original.Unit = "V"
	item, e := s.Retain(t.Context(), original)
	if e != nil {
		t.Fatal(e)
	}
	// Models caller uncertainty after an already committed Retain: exact input is
	// frozen, never regenerated, and the normal Store duplicate path resolves it.
	c.pending = []Reading{original}
	c.last = CollectionResult{Sampled: 1, Pending: 1}
	result, e := c.RetainPending(t.Context())
	if e != nil || result.Retained != 1 || result.Pending != 0 || calls != 0 {
		t.Fatal(result, e, calls)
	}
	rows, e := s.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 || rows[0].Sequence != item.Sequence {
		t.Fatal(rows, e)
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = c.PollOnce(t.Context()); !errors.Is(e, ErrSessionClosed) || calls != 0 {
		t.Fatal("closed session polled source", e, calls)
	}
}

func collectorLoopbackSource(t *testing.T, r Reading) driver.Driver {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, e := ln.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		req := make([]byte, 12)
		if _, e = io.ReadFull(conn, req); e != nil {
			return
		}
		if req[7] != 3 || binary.BigEndian.Uint16(req[10:]) != 1 {
			t.Error("unexpected read", req)
			return
		}
		response := []byte{req[0], req[1], 0, 0, 0, 5, req[6], 3, 2, 0, 42}
		conn.Write(response)
	}()
	addr := ln.Addr().(*net.TCPAddr)
	src, e := driver.New(edgeconfig.Device{ID: r.DeviceID, Profile: "modbus-tcp", Host: "127.0.0.1", NetPort: addr.Port, Address: 1, Points: []edgeconfig.Point{{ID: r.PointID, Register: 0, Func: 3, Type: "u16", Unit: r.Unit, Min: 0, Max: 100}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { src.Close() })
	return src
}
