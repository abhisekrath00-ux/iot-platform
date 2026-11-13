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
	Installing     bool      `json:"installing,omitempty"`
	Pending        []Result  `json:"pending_results,omitempty"`
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

func (c *Ctl) readState() (State, error) {
	var s State
	b, err := os.ReadFile(c.statePath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, nil
}
func (c *Ctl) State() State { s, _ := c.readState(); return s }

// Recover must run before loading managed config. A prepared state without a
// committed file/state pair is rolled back, not silently accepted after a crash.
func (c *Ctl) Recover(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("managed state unreadable: %w", err)
	}
	if !st.Installing {
		return nil
	}
	if err = c.restorePrevious(); err != nil {
		return fmt.Errorf("interrupted source apply restore: %w", err)
	}
	st.Installing = false
	st.ProbationUntil = time.Time{}
	st.Pending = append(st.Pending, Result{PushID: st.PushID, Version: st.Version, State: "rejected", Detail: "interrupted source write before application was committed; previous device list restored", At: now})
	return c.saveState(st)
}
func (c *Ctl) restorePrevious() error {
	prev, err := os.ReadFile(c.prevPath())
	if err == nil {
		return writeAtomic(c.Path, prev)
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(c.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
	if len(payload) > maxBytes+4096 {
		return rej("push is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return rej("bad json: %v", err)
	}
	if !c.Enabled {
		return rej("this box has not opted in (managed_devices: true in its config)")
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
	st, stateErr := c.readState()
	if stateErr != nil {
		return rej("could not read state: %v", stateErr)
	}
	if st.Installing {
		return rej("previous interrupted source write needs local recovery")
	}
	if p.Version <= st.Version {
		return rej("version %d is not newer than the applied version %d", p.Version, st.Version)
	}
	if st.ProbationUntil.After(now) {
		return rej("the previous change is still on probation")
	}
	if len(st.Pending) >= 31 {
		return rej("too many unacknowledged application results; wait for server delivery")
	}
	if _, err := Validate(p.DevicesYAML); err != nil {
		return rej("%v", err)
	}
	old, readErr := os.ReadFile(c.Path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return rej("could not read previous list: %v", readErr)
	}
	if readErr == nil {
		if err := writeAtomic(c.prevPath(), old); err != nil {
			return rej("could not keep previous list: %v", err)
		}
	} else if err := os.Remove(c.prevPath()); err != nil && !os.IsNotExist(err) {
		return rej("could not clear previous list: %v", err)
	}
	next := State{Installing: true, Version: p.Version, PrevVersion: st.Version, PushID: p.PushID, ProbationUntil: now.Add(Probation), Pending: st.Pending}
	// Save intent before touching the live file. Startup recovers an interrupted
	// pair even if it crashed between either of the following atomic renames.
	if err := c.saveState(next); err != nil {
		return rej("could not prepare state: %v", err)
	}
	if err := writeAtomic(c.Path, []byte("# pushed by the server; managed by HexThings\n"+p.DevicesYAML)); err != nil {
		if restoreErr := c.restorePrevious(); restoreErr != nil {
			return rej("write failed; local recovery required: %v", restoreErr)
		}
		if stateErr := c.saveState(st); stateErr != nil {
			return rej("write failed; state recovery required: %v", stateErr)
		}
		return rej("could not write: %v", err)
	}
	applied := Result{PushID: p.PushID, Version: p.Version, State: "applied", Detail: "written; polling reloads and rolls back if configuration cannot be loaded", At: now}
	next.Installing = false
	next.Pending = append(next.Pending, applied)
	if err := c.saveState(next); err != nil {
		if restoreErr := c.restorePrevious(); restoreErr != nil {
			return rej("state write failed; local recovery required: %v", restoreErr)
		}
		return rej("state write failed; previous list restored and startup recovery required: %v", err)
	}
	c.seen[p.PushID] = true
	return applied
}

// Settle runs at agent start (healthy=false: before the broker is up) and again when it is healthy.
// It returns a Result when something changed: confirmed once healthy within probation, rolled_back when
// probation ran out without health. needRestart is true after a rollback.
func (c *Ctl) Settle(healthy bool, now time.Time) (res *Result, needRestart bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.readState()
	if err != nil || st.Installing {
		return nil, false
	}
	if st.ProbationUntil.IsZero() {
		return nil, false
	}
	if healthy {
		result := Result{PushID: st.PushID, Version: st.Version, State: "confirmed", At: now}
		st.ProbationUntil = time.Time{}
		st.Pending = append(st.Pending, result)
		if err := c.saveState(st); err != nil {
			return nil, false
		}
		return &result, false
	}
	if now.Before(st.ProbationUntil) {
		return nil, false
	}
	if err := c.restorePrevious(); err != nil {
		return nil, false
	}
	failed := st.Version
	// Keep the failed version number so it can never be re-applied by a replay; a newer push is needed.
	result := Result{PushID: st.PushID, Version: failed, State: "rolled_back", Detail: "agent was not healthy within probation; previous device list restored", At: now}
	if err := c.saveState(State{Version: failed, PrevVersion: st.PrevVersion, PushID: st.PushID, Pending: append(st.Pending, result)}); err != nil {
		return nil, false
	}
	return &result, true
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

// PendingResults survives process restarts so applied always reaches the server
// before confirmed/rolled_back, including a failed publish before the restart.
func (c *Ctl) PendingResults() []Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Result(nil), c.State().Pending...)
}
func (c *Ctl) AcknowledgeResult(result Result) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.readState()
	if err != nil {
		return err
	}
	if len(st.Pending) == 0 {
		return nil
	}
	first := st.Pending[0]
	if first.PushID != result.PushID || first.State != result.State {
		return fmt.Errorf("result order changed")
	}
	st.Pending = st.Pending[1:]
	return c.saveState(st)
}
