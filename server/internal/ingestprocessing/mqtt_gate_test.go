package ingestprocessing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gateMessage struct {
	topic     string
	payload   []byte
	qos       byte
	retained  bool
	duplicate bool
	ack       func()
}

func (m *gateMessage) Duplicate() bool   { return m.duplicate }
func (m *gateMessage) Qos() byte         { return m.qos }
func (m *gateMessage) Retained() bool    { return m.retained }
func (m *gateMessage) Topic() string     { return m.topic }
func (m *gateMessage) MessageID() uint16 { return 7 }
func (m *gateMessage) Payload() []byte   { return m.payload }
func (m *gateMessage) Ack() {
	if m.ack != nil {
		m.ack()
	}
}
func newGate(t *testing.T, x Engine, limit int) *MQTTGate {
	t.Helper()
	g, err := NewMQTTGate(x, limit)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func gateInput(t *testing.T, e Envelope, ack func()) *gateMessage {
	return &gateMessage{topic: "t/a/g/g/telemetry", payload: wirePayload(t, e), qos: 1, ack: ack}
}
func TestMQTTGateDefaultOffAndZero(t *testing.T) {
	if _, err := NewMQTTGate(Engine{}, 1); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := NewMQTTGate(Engine{Enabled: true}, 1); !errors.Is(err, ErrTransport) {
		t.Fatal(err)
	}
	for _, g := range []*MQTTGate{nil, {}} {
		if r, err := g.Handle(t.Context(), nil); !errors.Is(err, ErrClosed) || r != (AdmissionReceipt{}) {
			t.Fatal(r, err)
		}
		if err := g.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
func TestMQTTGatePGCommitBeforeAckAndReplay(t *testing.T) {
	x, e := fixture(t)
	for _, limit := range []int{-1, 0, 65} {
		if _, err := NewMQTTGate(x, limit); !errors.Is(err, ErrTransport) {
			t.Fatal(limit, err)
		}
	}
	g := newGate(t, x, 2)
	calls := 0
	m := gateInput(t, e, func() {
		// This is a separate pool read during Ack, not uncommitted transaction state.
		counts(t, x.Pool, e.EventID, 1)
		historyCount(t, x, 0, "processing_started")
		calls++
	})
	r, err := g.Handle(t.Context(), m)
	if err != nil || !r.New || calls != 1 {
		t.Fatal(r, err, calls)
	}
	exec(t, x.Pool, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='c'`)
	m.duplicate = true
	r, err = g.Handle(t.Context(), m)
	if err != nil || r.New || r.State != "pending" || calls != 2 {
		t.Fatal(r, err, calls)
	}
	rec, err := x.Read(t.Context(), e.TenantID, e.EventID, e.ObservedAt)
	if err != nil || rec.Plan.Intents[0].Target != "synthetic@example.invalid" {
		t.Fatal(rec, err)
	}
	if err := g.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r, err = g.Handle(t.Context(), m); !errors.Is(err, ErrClosed) || r != (AdmissionReceipt{}) || calls != 2 {
		t.Fatal(r, err, calls)
	}
}
func TestMQTTGatePGRefusalNeverAck(t *testing.T) {
	for _, mode := range []string{"qos0", "qos2", "retained", "malformed", "foreign", "canceled", "insert", "commit", "schema", "conflict", "legacy", "nil", "typednil"} {
		t.Run(mode, func(t *testing.T) {
			x, e := fixture(t)
			g := newGate(t, x, 1)
			calls := 0
			m := gateInput(t, e, func() { calls++ })
			ctx := t.Context()
			switch mode {
			case "qos0":
				m.qos = 0
			case "qos2":
				m.qos = 2
			case "retained":
				m.retained = true
			case "malformed":
				m.payload = []byte(`{}`)
			case "foreign":
				m.topic = "t/b/g/g/telemetry"
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "insert":
				exec(t, x.Pool, `CREATE FUNCTION fail_gate() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic insert failure';END$$;CREATE TRIGGER fail_gate BEFORE INSERT ON ingest_processing_v1 FOR EACH ROW EXECUTE FUNCTION fail_gate()`)
			case "commit":
				exec(t, x.Pool, `CREATE FUNCTION fail_gate() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic commit failure';END$$;CREATE CONSTRAINT TRIGGER fail_gate AFTER INSERT ON ingest_processing_v1 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_gate()`)
			case "schema":
				exec(t, x.Pool, `DROP TABLE ingest_processing_v1`)
			case "conflict":
				mustAdmit(t, x, e)
				e.Value++
				m.payload = wirePayload(t, e)
			case "legacy":
				exec(t, x.Pool, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES($1,'a','g','d','p',$2,42,'','measured',1)`, e.EventID, e.ObservedAt)
			case "typednil":
				m = nil
			}
			var r AdmissionReceipt
			var err error
			if mode == "nil" {
				r, err = g.Handle(ctx, nil)
			} else {
				r, err = g.Handle(ctx, m)
			}
			if err == nil || r != (AdmissionReceipt{}) || calls != 0 {
				t.Fatal(r, err, calls)
			}
			if mode == "insert" || mode == "commit" {
				counts(t, x.Pool, e.EventID, 0)
			}
			historyCount(t, x, 0, "processing_started")
			if err = g.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMQTTGatePGBoundedDrainAndClosedTimeout(t *testing.T) {
	x, e := fixture(t)
	g := newGate(t, x, 1)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	m := gateInput(t, e, func() { close(entered); <-release })
	go func() { _, err := g.Handle(t.Context(), m); done <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("ack not reached")
	}
	e2 := e
	e2.EventID = "busy"
	var calls atomic.Int32
	blocked := gateInput(t, e2, func() { calls.Add(1) })
	if r, err := g.Handle(t.Context(), blocked); !errors.Is(err, ErrBusy) || r != (AdmissionReceipt{}) {
		t.Fatal(r, err)
	}
	counts(t, x.Pool, "busy", 0)
	c, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.Close(c); !errors.Is(err, context.Canceled) {
		t.Fatal("drain falsely completed", err)
	}
	if r, err := g.Handle(t.Context(), blocked); !errors.Is(err, ErrClosed) || r != (AdmissionReceipt{}) {
		t.Fatal(r, err)
	}
	closed := make(chan error, 2)
	for range 2 {
		go func() { closed <- g.Close(t.Context()) }()
	}
	select {
	case err := <-closed:
		t.Fatal("close skipped active ACK", err)
	default:
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("refused message ACKed")
	}
	counts(t, x.Pool, "busy", 0)
	historyCount(t, x, 0, "processing_started")
}
func TestMQTTGatePGAckPanicStillDrains(t *testing.T) {
	x, e := fixture(t)
	g := newGate(t, x, 1)
	m := gateInput(t, e, func() { panic("synthetic ACK interruption") })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic hidden")
			}
		}()
		_, _ = g.Handle(t.Context(), m)
	}()
	counts(t, x.Pool, e.EventID, 1)
	if err := g.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	g = newGate(t, x, 1)
	calls := 0
	m.ack = func() { calls++ }
	r, err := g.Handle(t.Context(), m)
	if err != nil || r.New || calls != 1 {
		t.Fatal(r, err, calls)
	}
	historyCount(t, x, 0, "processing_started")
}

func TestMQTTGatePGBeforeCommitCapacityCancelAndDrain(t *testing.T) {
	x, e := fixture(t)
	g := newGate(t, x, 1)
	tx, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, err := g.Handle(c, gateInput(t, e, func() { calls.Add(1) }))
		if r != (AdmissionReceipt{}) {
			done <- errors.New("positive receipt after cancellation")
			return
		}
		done <- err
	}()
	// Observe the actual blocked admission, not a guessed delay.
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	waiting := false
	for !waiting {
		select {
		case <-deadline:
			t.Fatal("admission did not reach lock")
		case <-tick.C:
			if err = x.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event='advisory' AND query LIKE '%731492068214%')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
		}
	}
	e2 := e
	e2.EventID = "before-commit-busy"
	if r, err := g.Handle(t.Context(), gateInput(t, e2, func() { calls.Add(1) })); !errors.Is(err, ErrBusy) || r != (AdmissionReceipt{}) {
		t.Fatal(r, err)
	}
	closing, cancelClose := context.WithCancel(t.Context())
	cancelClose()
	if err := g.Close(closing); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled admission succeeded")
	}
	if err = g.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("ACK before commit")
	}
	counts(t, x.Pool, e.EventID, 0)
	counts(t, x.Pool, e2.EventID, 0)
}
