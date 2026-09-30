// Package localui serves a read-only status page for the gateway itself.
//
// It exposes no control paths and no secrets: only what an installer needs to
// see on site (is it enrolled, is the broker reachable, are devices reading,
// how much is buffered). It binds to loopback by default.
package localui

import (
	"sort"
	"sync"
	"time"
)

// DeviceStatus is the last known state of one configured device.
type DeviceStatus struct {
	ID        string             `json:"id"`
	Driver    string             `json:"driver"`
	LastOK    time.Time          `json:"last_ok,omitempty"`
	LastError string             `json:"last_error,omitempty"`
	LastErrAt time.Time          `json:"last_error_at,omitempty"`
	Reads     uint64             `json:"reads"`
	Errors    uint64             `json:"errors"`
	LastValue map[string]float64 `json:"last_values,omitempty"`
}

// Tracker collects status from the poll loops. Safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	started time.Time
	devices map[string]*DeviceStatus
}

func NewTracker() *Tracker {
	return &Tracker{started: time.Now(), devices: map[string]*DeviceStatus{}}
}

func (t *Tracker) dev(id, driver string) *DeviceStatus {
	d, ok := t.devices[id]
	if !ok {
		d = &DeviceStatus{ID: id, Driver: driver, LastValue: map[string]float64{}}
		t.devices[id] = d
	}
	return d
}

// Reset clears devices, used when a fleet config reload swaps the device set.
func (t *Tracker) Reset() {
	t.mu.Lock()
	t.devices = map[string]*DeviceStatus{}
	t.mu.Unlock()
}

// Register makes a device visible before its first poll.
func (t *Tracker) Register(id, driver string) {
	t.mu.Lock()
	t.dev(id, driver)
	t.mu.Unlock()
}

func (t *Tracker) RecordRead(id, driver string, values map[string]float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.dev(id, driver)
	d.Reads++
	d.LastOK = time.Now().UTC()
	d.LastError = ""
	for k, v := range values {
		d.LastValue[k] = v
	}
}

func (t *Tracker) RecordError(id, driver string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.dev(id, driver)
	d.Errors++
	d.LastError = err.Error()
	d.LastErrAt = time.Now().UTC()
}

// Snapshot returns a stable, sorted copy.
func (t *Tracker) Snapshot() (time.Time, []DeviceStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]DeviceStatus, 0, len(t.devices))
	for _, d := range t.devices {
		c := *d
		c.LastValue = make(map[string]float64, len(d.LastValue))
		for k, v := range d.LastValue {
			c.LastValue[k] = v
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return t.started, out
}
