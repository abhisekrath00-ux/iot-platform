package ingestprocessing

import (
	"context"
	"errors"
	"reflect"
	"sync"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var (
	ErrTransport = errors.New("staged MQTT admission requires a non-retained QoS1 message")
	ErrBusy      = errors.New("staged MQTT admission at capacity; message not acknowledged")
	ErrClosed    = errors.New("staged MQTT admission gate closed; message not acknowledged")
)

// MQTTGate is a bounded synchronous admission adapter, not a subscriber or an
// enable switch. A constructed gate must not be copied. The caller MUST
// configure manual ACK, a stable persistent
// session, authenticated topic ACLs and an all-handler failure policy BEFORE
// considering runtime wiring. No production code constructs this gate.
//
// Refused messages are NOT queued, discarded or ACKed here. Returning an error
// alone does not make Paho redeliver: the caller still owns session shutdown and
// durable broker/publisher recovery. This gate never retries or executes effects.
// Each successful call invokes that message's Ack once, after committed/exact
// replay evidence. Invocation does NOT prove PUBACK reached the broker, nor
// provide an application receipt to the edge or a provider delivery receipt.
// Duplicate deliveries may each need their own protocol ACK, but share the same
// stable input identity and never execute any derived work here.
type MQTTGate struct {
	mu            sync.Mutex
	limit, active int
	closing       bool
	drained       chan struct{}
	engine        Engine
}

// NewMQTTGate makes an explicit default-off adapter. A disabled engine refuses
// before inspecting its pool. No environment, connection or goroutine is used.
func NewMQTTGate(engine Engine, limit int) (*MQTTGate, error) {
	if !engine.Enabled {
		return nil, ErrDisabled
	}
	if engine.Pool == nil || limit < 1 || limit > 64 {
		return nil, ErrTransport
	}
	return &MQTTGate{engine: engine, limit: limit, drained: make(chan struct{})}, nil
}

func (g *MQTTGate) enter() error {
	if g == nil {
		return ErrClosed
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing || g.drained == nil {
		return ErrClosed
	}
	if g.active >= g.limit {
		return ErrBusy
	}
	g.active++
	return nil
}
func (g *MQTTGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.closing && g.active == 0 {
		close(g.drained)
	}
}

// Handle returns zero evidence on error and never ACKs a refused/failed input.
// Payload bytes are copied before admission. Callers must not mutate a message
// concurrently with this call. An ACK panic propagates with capacity released;
// an interrupted handler is uncertain, never permission for a new identity.
func (g *MQTTGate) Handle(ctx context.Context, m mqtt.Message) (AdmissionReceipt, error) {
	if err := g.enter(); err != nil {
		return AdmissionReceipt{}, err
	}
	defer g.leave()
	if ctx == nil {
		return AdmissionReceipt{}, ErrTransport
	}
	if err := ctx.Err(); err != nil {
		return AdmissionReceipt{}, err
	}
	if m == nil {
		return AdmissionReceipt{}, ErrTransport
	}
	v := reflect.ValueOf(m)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		if v.IsNil() {
			return AdmissionReceipt{}, ErrTransport
		}
	}
	if m.Qos() != 1 || m.Retained() {
		return AdmissionReceipt{}, ErrTransport
	}
	topic, payload := m.Topic(), m.Payload()
	if len(topic) > 540 || len(payload) > 8192 {
		return AdmissionReceipt{}, ErrWire
	}
	r, err := g.engine.AdmitMQTT(ctx, topic, append([]byte(nil), payload...))
	if err != nil {
		return AdmissionReceipt{}, err
	}
	// Cancellation observed here yields no positive disposition even if commit
	// won the race. Cancellation after this check may race with ACK invocation.
	// Identical explicit reconciliation is safe; a new identity is not.
	if err = ctx.Err(); err != nil {
		return AdmissionReceipt{}, err
	}
	m.Ack()
	return r, nil
}

// Close permanently refuses new calls and waits for all admitted handlers,
// including their ACK invocation, to return. Timeout leaves the gate CLOSED;
// it does not cancel in-flight transactions or claim they rolled back. A later
// Close may wait for actual drain. Caller-owned contexts bound DB/handler work.
func (g *MQTTGate) Close(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if ctx == nil {
		return ErrTransport
	}
	g.mu.Lock()
	if g.drained == nil {
		g.mu.Unlock()
		return nil
	}
	if !g.closing {
		g.closing = true
		if g.active == 0 {
			close(g.drained)
		}
	}
	drained := g.drained
	g.mu.Unlock()
	select {
	case <-drained:
		return nil
	default:
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
