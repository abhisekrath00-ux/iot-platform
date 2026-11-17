package admissionoutbox

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"github.com/google/uuid"
)

var ErrCollectorBusy = errors.New("staged collector operation already running")
var ErrCollectorPending = errors.New("staged collector has unresolved exact input; no new poll started")
var ErrCollectorSample = errors.New("staged collector requires one complete bounded configured sample")

// PollReader is deliberately narrower than driver.Driver: collector never calls
// a write, actuator, rule, discovery or Close method. Caller owns source lifetime
// and must Stop/drain before closing/replacing it. Poll must honor cancellation
// or have its own bounded IO; Stop timeout cannot pretend it stopped a source.
type PollReader interface {
	Poll(context.Context) ([]driver.Reading, error)
}
type CollectorConfig struct {
	Enabled  bool
	Session  *Session
	Reader   PollReader
	DeviceID string
	Points   map[string]string // exact point ID -> expected unit, 1-256
}
type CollectionResult struct {
	Sampled  int `json:"sampled"`
	Retained int `json:"retained"`
	Pending  int `json:"pending"` // in memory until Retain succeeds, NOT durable
}
type CollectorStatus struct {
	State   string           `json:"state"`
	Active  bool             `json:"active"`
	Pending int              `json:"pending"`
	Last    CollectionResult `json:"last"`
	Outcome string           `json:"outcome"`
}

// Collector is an explicit read-to-retain adapter, not a poll scheduler or runtime
// switch. No startup constructs it. It never sends. It accepts one whole exact
// configured sample, assigns fresh event IDs ONCE and a single UTC microsecond
// post-poll observation time, then retains sequentially. This is gateway sample
// time, NOT a meter's clock or proof of clock synchronization. Error after Put
// may be uncertain; the SAME frozen pending reading remains for explicit
// RetainPending. No new poll is allowed until it resolves. Pending is volatile:
// process death before a confirmed Put can lose that reading. No powerloss claim.
type Collector struct {
	mu              sync.Mutex
	session         *Session
	reader          PollReader
	device          string
	points          map[string]string
	active, stopped bool
	done            chan struct{}
	pending         []Reading
	last            CollectionResult
	outcome         string
}

func NewCollector(c CollectorConfig) (*Collector, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if c.Session == nil || c.Reader == nil || !validID(c.DeviceID) || len(c.Points) < 1 || len(c.Points) > 256 {
		return nil, ErrInput
	}
	v := reflect.ValueOf(c.Reader)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface || v.Kind() == reflect.Func || v.Kind() == reflect.Map || v.Kind() == reflect.Chan || v.Kind() == reflect.Slice) && v.IsNil() {
		return nil, ErrInput
	}
	points := make(map[string]string, len(c.Points))
	for id, unit := range c.Points {
		if !validID(id) || len(unit) > 64 || !utf8.ValidString(unit) {
			return nil, ErrInput
		}
		points[id] = unit
	}
	c.Session.mu.Lock()
	closed := c.Session.closing
	c.Session.mu.Unlock()
	if closed {
		return nil, ErrSessionClosed
	}
	return &Collector{session: c.Session, reader: c.Reader, device: c.DeviceID, points: points, outcome: "idle"}, nil
}
func (c *Collector) enter(ctx context.Context, newPoll bool) error {
	if c == nil {
		return ErrSessionClosed
	}
	if ctx == nil {
		return ErrInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped && newPoll {
		return ErrSessionClosed
	}
	if c.active {
		return ErrCollectorBusy
	}
	if newPoll && len(c.pending) > 0 {
		return ErrCollectorPending
	}
	c.active = true
	c.done = make(chan struct{})
	return nil
}
func (c *Collector) leave() { c.mu.Lock(); c.active = false; close(c.done); c.mu.Unlock() }
func (c *Collector) PollOnce(ctx context.Context) (CollectionResult, error) {
	if err := c.enter(ctx, true); err != nil {
		return CollectionResult{}, err
	}
	defer c.leave()
	c.session.mu.Lock()
	closed := c.session.closing
	c.session.mu.Unlock()
	if closed {
		c.setOutcome("sample-refused")
		return CollectionResult{}, ErrSessionClosed
	}
	samples, err := c.reader.Poll(ctx)
	if err != nil {
		c.setOutcome("sample-refused")
		return CollectionResult{}, err
	}
	if err = ctx.Err(); err != nil {
		c.setOutcome("sample-refused")
		return CollectionResult{}, err
	}
	if len(samples) != len(c.points) {
		c.setOutcome("sample-refused")
		return CollectionResult{}, ErrCollectorSample
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	pending := make([]Reading, 0, len(samples))
	seen := make(map[string]bool, len(samples))
	for _, sample := range samples {
		unit, ok := c.points[sample.PointID]
		if !ok || seen[sample.PointID] || sample.DeviceID != c.device || unit != sample.Unit {
			c.setOutcome("sample-refused")
			return CollectionResult{}, ErrCollectorSample
		}
		seen[sample.PointID] = true
		r := Reading{EventID: uuid.NewString(), TenantID: c.session.tenant, GatewayID: c.session.gateway, DeviceID: c.device, PointID: sample.PointID, ObservedAt: observed, Value: sample.Value, Unit: sample.Unit, Quality: "measured", SchemaVersion: 1}
		if _, _, err = canonical(r); err != nil {
			c.setOutcome("sample-refused")
			return CollectionResult{}, ErrCollectorSample
		}
		pending = append(pending, r)
	}
	c.mu.Lock()
	c.pending = pending
	c.last = CollectionResult{Sampled: len(pending), Pending: len(pending)}
	c.mu.Unlock()
	return c.retain(ctx)
}
func (c *Collector) RetainPending(ctx context.Context) (CollectionResult, error) {
	if err := c.enter(ctx, false); err != nil {
		return CollectionResult{}, err
	}
	defer c.leave()
	return c.retain(ctx)
}
func (c *Collector) retain(ctx context.Context) (CollectionResult, error) {
	for {
		c.mu.Lock()
		if len(c.pending) == 0 {
			c.outcome = "retained"
			r := c.last
			c.mu.Unlock()
			return r, nil
		}
		reading := c.pending[0]
		c.mu.Unlock()
		if _, err := c.session.Retain(ctx, reading); err != nil {
			c.mu.Lock()
			c.outcome = "retention-unresolved"
			r := c.last
			c.mu.Unlock()
			return r, err
		}
		c.mu.Lock()
		c.pending = c.pending[1:]
		c.last.Retained++
		c.last.Pending = len(c.pending)
		c.mu.Unlock()
	}
}
func (c *Collector) setOutcome(s string) {
	c.mu.Lock()
	c.outcome = s
	c.last = CollectionResult{}
	c.mu.Unlock()
}
func (c *Collector) Status() CollectorStatus {
	if c == nil {
		return CollectorStatus{State: "stopped"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := "open"
	if c.stopped {
		state = "stopped"
		if c.active {
			state = "stopping"
		}
	}
	return CollectorStatus{State: state, Active: c.active, Pending: len(c.pending), Last: c.last, Outcome: c.outcome}
}

// Stop permanently prevents new reads and drains the current call. Explicit
// RetainPending remains allowed after Stop because it does not touch the source. It
// does NOT discard/flush pending, close the source/session, cancel an in-flight
// read or send anything. Caller must inspect pending before replacing objects.
// Timeout stays stopping; do not close the source until Stop later returns nil.
func (c *Collector) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return ErrInput
	}
	c.mu.Lock()
	c.stopped = true
	active, done := c.active, c.done
	c.mu.Unlock()
	if !active {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
