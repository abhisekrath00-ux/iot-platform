// Package cfgpush applies device templates pushed from the server. What is pushed is only the device list
// (connection details and point maps per device), never broker, TLS, identity or security settings, and
// only when the box's own config sets managed_devices: true. A push must be signed by the control plane,
// for this tenant and gateway, unexpired, newer than the last applied version, and must pass the same
// checks as a hand-written config. The previous list is kept; if the agent does not come back healthy
// after the change it is put back (probation). Nothing here restarts the machine or runs commands.
package cfgpush

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

const (
	maxBytes   = 256 << 10
	maxDevices = 200
	maxTTL     = 24 * time.Hour
	skew       = 60 * time.Second
	Probation  = 3 * time.Minute
)

// Push is the wire format on t/<tenant>/g/<gw>/config.
type Push struct {
	PushID      string    `json:"push_id"`
	TenantID    string    `json:"tenant_id"`
	Serial      string    `json:"gateway_serial"`
	Version     int       `json:"version"`
	DevicesYAML string    `json:"devices_yaml"`
	SHA256      string    `json:"sha256"`
	ApprovedBy  string    `json:"approved_by,omitempty"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Signature   string    `json:"signature"`
}

// Result is published on t/<tenant>/g/<gw>/config/result.
type Result struct {
	PushID  string    `json:"push_id"`
	Version int       `json:"version"`
	State   string    `json:"state"` // applied | rejected | rolled_back | confirmed
	Detail  string    `json:"detail,omitempty"`
	At      time.Time `json:"at"`
}

// State survives restarts.
type State struct {
	Version        int       `json:"version"`
	PrevVersion    int       `json:"prev_version"`
	PushID         string    `json:"push_id,omitempty"`
	ProbationUntil time.Time `json:"probation_until,omitempty"`
}

// Canonical is the byte string that is signed. server/internal/fleet/cfgpush.go must match it.
func Canonical(p Push) []byte {
	return []byte(strings.Join([]string{"hexmon-config-push-v1", p.TenantID, p.Serial, strconv.Itoa(p.Version), p.PushID, p.SHA256, p.ExpiresAt.UTC().Format(time.RFC3339)}, "\n"))
}

// Ctl applies pushes for one gateway.
type Ctl struct {
	Path    string // managed-devices.yaml
	Key     ed25519.PublicKey
	Tenant  string
	Serial  string
	Enabled bool // config managed_devices
	mu      sync.Mutex
	seen    map[string]bool
}

func New(path string, key ed25519.PublicKey, tenant, serial string, enabled bool) *Ctl {
	return &Ctl{Path: path, Key: key, Tenant: tenant, Serial: serial, Enabled: enabled, seen: map[string]bool{}}
}

func (c *Ctl) statePath() string { return c.Path + ".state.json" }
func (c *Ctl) prevPath() string  { return c.Path + ".prev" }

func (c *Ctl) State() State {
	var s State
	if b, err := os.ReadFile(c.statePath()); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func (c *Ctl) saveState(s State) error {
	b, _ := json.Marshal(s)
	return writeAtomic(c.statePath(), b)
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate checks a device list the way the agent would load it and returns it.
func Validate(devicesYAML string) ([]config.Device, error) {
	if len(devicesYAML) > maxBytes {
		return nil, errors.New("device list is too large")
	}
	var f struct {
		Devices []config.Device `yaml:"devices"`
	}
	dec := yaml.NewDecoder(strings.NewReader(devicesYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("device list does not parse: %w", err)
	}
	if len(f.Devices) > maxDevices {
		return nil, fmt.Errorf("more than %d devices", maxDevices)
	}
	seen := map[string]bool{}
	for _, d := range f.Devices {
		if d.ID == "" || seen[d.ID] {
			return nil, fmt.Errorf("device id %q is empty or repeated", d.ID)
		}
		seen[d.ID] = true
		if d.Interval <= 0 {
			return nil, fmt.Errorf("device %s: interval required", d.ID)
		}
		for _, p := range d.Points {
			if p.Max <= p.Min {
				return nil, fmt.Errorf("device %s point %s: validation range required", d.ID, p.ID)
			}
		}
	}
	if is := config.Lint(&config.Config{Devices: f.Devices}, runtime.GOOS); config.HasError(is) {
		for _, i := range is {
			if i.Error && !strings.HasPrefix(i.Msg, "mqtt") && !strings.HasPrefix(i.Msg, "gateway") && !strings.HasPrefix(i.Msg, "tenant") {
				return nil, fmt.Errorf("lint: %s", i.String())
			}
		}
	}
	return f.Devices, nil
}

// Apply handles one push payload. Rejections leave the current list untouched.
func (c *Ctl) Apply(payload []byte, now time.Time) Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	var p Push
	rej := func(format string, a ...any) Result {
		return Result{PushID: p.PushID, Version: p.Version, State: "rejected", Detail: fmt.Sprintf(format, a...), At: now}
	}
	if !c.Enabled {
		return rej("this box has not opted in (managed_devices: true in its config)")
	}
	if len(payload) > maxBytes+4096 {
		return rej("push is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return rej("bad json: %v", err)
	}
	if c.Key == nil {
		return rej("no fleet_public_key configured; pushes must be signed")
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(c.Key, Canonical(p), sig) {
		return rej("signature does not verify")
	}
	if p.TenantID != c.Tenant || p.Serial != c.Serial {
		return rej("push is for another tenant or gateway")
	}
	if p.PushID == "" || len(p.PushID) > 64 {
		return rej("push_id missing")
	}
	if now.After(p.ExpiresAt) || p.IssuedAt.After(now.Add(skew)) || p.ExpiresAt.Sub(p.IssuedAt) > maxTTL {
		return rej("push is expired or has a bad lifetime")
	}
	if c.seen[p.PushID] {
		return rej("push already used")
	}
	h := sha256.Sum256([]byte(p.DevicesYAML))
	if hex.EncodeToString(h[:]) != p.SHA256 {
		return rej("checksum does not match the device list")
	}
	st := c.State()
	if p.Version <= st.Version {
		return rej("version %d is not newer than the applied version %d", p.Version, st.Version)
	}
	if st.ProbationUntil.After(now) {
		return rej("the previous change is still on probation")
	}
	if _, err := Validate(p.DevicesYAML); err != nil {
		return rej("%v", err)
	}
	if old, err := os.ReadFile(c.Path); err == nil {
		if err := writeAtomic(c.prevPath(), old); err != nil {
			return rej("could not keep the previous list: %v", err)
		}
	} else {
		os.Remove(c.prevPath())
	}
	if err := writeAtomic(c.Path, []byte("# pushed by the server; managed by HexThings\n"+p.DevicesYAML)); err != nil {
		return rej("could not write: %v", err)
	}
	c.seen[p.PushID] = true
	if err := c.saveState(State{Version: p.Version, PrevVersion: st.Version, PushID: p.PushID, ProbationUntil: now.Add(Probation)}); err != nil {
		return rej("could not save state: %v", err)
	}
	return Result{PushID: p.PushID, Version: p.Version, State: "applied", Detail: "written; the agent restarts to use it and rolls back if it does not come up healthy", At: now}
}

// Settle runs at agent start (healthy=false: before the broker is up) and again when it is healthy.
// It returns a Result when something changed: confirmed once healthy within probation, rolled_back when
// probation ran out without health. needRestart is true after a rollback.
func (c *Ctl) Settle(healthy bool, now time.Time) (res *Result, needRestart bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.State()
	if st.ProbationUntil.IsZero() {
		return nil, false
	}
	if healthy {
		st.ProbationUntil = time.Time{}
		c.saveState(st)
		return &Result{PushID: st.PushID, Version: st.Version, State: "confirmed", At: now}, false
	}
	if now.Before(st.ProbationUntil) {
		return nil, false
	}
	if prev, err := os.ReadFile(c.prevPath()); err == nil {
		writeAtomic(c.Path, prev)
	} else {
		os.Remove(c.Path)
	}
	failed := st.Version
	// Keep the failed version number so it can never be re-applied by a replay; a newer push is needed.
	c.saveState(State{Version: failed, PrevVersion: st.PrevVersion, PushID: st.PushID})
	return &Result{PushID: st.PushID, Version: failed, State: "rolled_back", Detail: "agent was not healthy within probation; previous device list restored", At: now}, true
}

// Preflight runs before the config is loaded. If the last push is still on probation and its file does
// not pass validation (so the agent could not start with it), the previous list is restored at once.
func (c *Ctl) Preflight(now time.Time) *Result {
	c.mu.Lock()
	st := c.State()
	c.mu.Unlock()
	if st.ProbationUntil.IsZero() {
		return nil
	}
	b, err := os.ReadFile(c.Path)
	if err == nil {
		if _, err = Validate(string(b)); err == nil {
			return nil
		}
	}
	if os.IsNotExist(err) {
		return nil
	}
	res, _ := c.Settle(false, st.ProbationUntil.Add(time.Second))
	if res != nil {
		res.At = now
	}
	return res
}
