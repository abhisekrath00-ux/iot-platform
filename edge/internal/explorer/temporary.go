package explorer

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"github.com/google/uuid"
)

// Connect deliberately excludes registers, write options, discovery, commands,
// credentials and persistence. Opening an RTU connection can change local line
// framing. It sends no Modbus request; only an explicit Start performs reads.
type Connect struct {
	Transport string `json:"transport"`
	Port      string `json:"port"`
	Baud      int    `json:"baud"`
	DataBits  int    `json:"data_bits"`
	StopBits  int    `json:"stop_bits"`
	Parity    string `json:"parity"`
	Host      string `json:"host"`
	NetPort   int    `json:"net_port"`
	Unit      int    `json:"unit"`
}

func (c Connect) device() (config.Device, Connection, error) {
	d := config.Device{ID: "temporary-explorer", Profile: "modbus-generic", Address: c.Unit, Port: c.Port, Baud: c.Baud, DataBits: c.DataBits, StopBits: c.StopBits, Parity: c.Parity}
	info := Connection{Name: "Temporary explorer", Transport: c.Transport, Unit: c.Unit, Temporary: true}
	if c.Unit < 1 || c.Unit > 247 {
		return d, info, fmt.Errorf("choose a unit address from 1 to 247")
	}
	switch c.Transport {
	case "rtu":
		if c.Host != "" || c.NetPort != 0 || c.Port == "" || len(c.Port) > 256 || strings.ContainsAny(c.Port, "\x00\r\n") || c.Baud < 300 || c.Baud > 115200 || c.DataBits != 8 || (c.StopBits != 1 && c.StopBits != 2) || (c.Parity != "none" && c.Parity != "even" && c.Parity != "odd") {
			return d, info, fmt.Errorf("choose an exact serial port, 300-115200 baud, 8 data bits, 1/2 stop bits and none/even/odd parity")
		}
		info.Endpoint = c.Port
	case "tcp":
		ip := net.ParseIP(c.Host)
		if ip == nil || (!ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()) || c.NetPort < 1 || c.NetPort > 65535 || c.Port != "" || c.Baud != 0 || c.DataBits != 0 || c.StopBits != 0 || c.Parity != "" {
			return d, info, fmt.Errorf("enter a literal unicast IP address and TCP port; no serial fields")
		}
		d.Profile = "modbus-tcp"
		d.Host = ip.String()
		d.NetPort = c.NetPort
		info.Endpoint = net.JoinHostPort(d.Host, fmt.Sprint(c.NetPort))
	default:
		return d, info, fmt.Errorf("choose RTU or TCP")
	}
	return d, info, nil
}

type temporary struct {
	close         func() error
	created, last time.Time
}

// EnableTemporary opts this registry into one temporary connection at a time.
// Shutdown drains reads and closes the handle. Browser abandonment expires in
// at most ten idle minutes; an active connection has a thirty-minute hard cap.
// A page reload never reopens it and no temporary connection is saved/applied.
func (r *Registry) EnableTemporary(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	r.mu.Lock()
	r.temporaryEnabled = true
	r.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				r.closeTemporary()
				return
			case now := <-ticker.C:
				r.expireTemporary(now)
			}
		}
	}()
	return done
}

func (r *Registry) OpenTemporary(ctx context.Context, c Connect) (Connection, error) {
	d, info, err := c.device()
	if err != nil {
		return Connection{}, err
	}
	if !r.mu.TryLock() {
		return Connection{}, fmt.Errorf("a read or device change is in progress; stop and try again")
	}
	defer r.mu.Unlock()
	if !r.temporaryEnabled {
		return Connection{}, fmt.Errorf("temporary connections are disabled")
	}
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	for _, e := range r.entries {
		if e.temp != nil {
			return Connection{}, fmt.Errorf("disconnect the current temporary connection first")
		}
		if info.Transport == e.info.Transport && info.Endpoint == e.info.Endpoint {
			return Connection{}, fmt.Errorf("this endpoint already belongs to a device; select its existing connection")
		}
	}
	factory := r.factory
	if factory == nil {
		factory = driver.New
	}
	dvr, err := factory(d)
	if err != nil {
		return Connection{}, err
	}
	reader, ok := dvr.(driver.RangeReader)
	if !ok {
		dvr.Close()
		return Connection{}, fmt.Errorf("connection has no bounded read support")
	}
	if err := ctx.Err(); err != nil {
		dvr.Close()
		return Connection{}, err
	}
	if r.entries == nil {
		r.entries = make(map[string]entry)
	}
	info.ID = uuid.NewString()
	now := time.Now()
	info.ExpiresAt = now.Add(30 * time.Minute)
	r.entries[info.ID] = entry{info: info, reader: reader, temp: &temporary{close: dvr.Close, created: now, last: now}}
	return info, nil
}
func (r *Registry) DisconnectTemporary(id string) error {
	if !r.mu.TryLock() {
		return fmt.Errorf("a read is in progress; stop and try again")
	}
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.temp == nil {
		return fmt.Errorf("temporary connection expired or not found")
	}
	delete(r.entries, id)
	return e.temp.close()
}
func (r *Registry) closeTemporary() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.temporaryEnabled = false
	for id, e := range r.entries {
		if e.temp != nil {
			delete(r.entries, id)
			e.temp.close()
		}
	}
}
func expired(t *temporary, now time.Time) bool {
	return !now.Before(t.created.Add(30*time.Minute)) || !now.Before(t.last.Add(10*time.Minute))
}
func (r *Registry) expireTemporary(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.entries {
		if e.temp != nil && expired(e.temp, now) {
			delete(r.entries, id)
			e.temp.close()
		}
	}
}
