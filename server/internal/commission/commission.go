// Package commission carries the guided-commissioning domain logic shared by
// the API handlers: probe validation, QR claim payloads, and step-state
// derivation. Probe payloads cross the MQTT boundary to the edge agent, so
// every field is validated here before publish (defense in depth: the edge
// re-clamps before touching hardware).
package commission

import (
	"fmt"
	"net/url"
	"regexp"
)

// Probe is the read-only port-test request sent to a gateway. Read-only by
// construction: it can only issue Modbus read functions (1-4), never write
// functions, so no actuation path exists and the four-eyes gate in
// docs/security.md does not apply.
type Probe struct {
	SessionID string `json:"session_id"`
	Port      string `json:"port"`
	Baud      int    `json:"baud"`
	DataBits  int    `json:"data_bits"`
	StopBits  int    `json:"stop_bits"`
	Parity    string `json:"parity"`
	Address   int    `json:"address"`
	Func      int    `json:"func"`
	Register  int    `json:"register"`
	Count     int    `json:"count"`
	Type      string `json:"type"`
	WordOrder string `json:"word_order"`
	TimeoutMs int    `json:"timeout_ms"`
}

var portPath = regexp.MustCompile(`^/dev/tty[A-Za-z0-9._-]{1,60}$`)

var validBaud = map[int]bool{1200: true, 2400: true, 4800: true, 9600: true, 19200: true, 38400: true, 57600: true, 115200: true}

// ValidateProbe rejects anything outside the read-only diagnostic envelope.
func ValidateProbe(p Probe) error {
	if !portPath.MatchString(p.Port) {
		return fmt.Errorf("port must look like /dev/ttyUSB0 (got %q)", p.Port)
	}
	if !validBaud[p.Baud] {
		return fmt.Errorf("baud %d not in standard set", p.Baud)
	}
	if p.DataBits < 5 || p.DataBits > 8 {
		return fmt.Errorf("data_bits must be 5-8")
	}
	if p.StopBits != 1 && p.StopBits != 2 {
		return fmt.Errorf("stop_bits must be 1 or 2")
	}
	switch p.Parity {
	case "none", "odd", "even":
	default:
		return fmt.Errorf("parity must be none|odd|even")
	}
	if p.Address < 1 || p.Address > 247 {
		return fmt.Errorf("modbus address must be 1-247")
	}
	if p.Func < 1 || p.Func > 4 {
		return fmt.Errorf("func must be a read function 1-4")
	}
	if p.Register < 0 || p.Register > 65535 {
		return fmt.Errorf("register must be 0-65535")
	}
	if p.Count < 1 || p.Count > 32 || p.Register+p.Count > 65536 {
		return fmt.Errorf("count must be 1-32 within register space")
	}
	switch p.Type {
	case "", "u16", "i16", "u32", "i32", "f32", "bool":
	default:
		return fmt.Errorf("type %q unsupported", p.Type)
	}
	switch p.WordOrder {
	case "", "abcd", "badc", "cdab", "dcba":
	default:
		return fmt.Errorf("word_order %q unsupported", p.WordOrder)
	}
	if p.TimeoutMs < 0 || p.TimeoutMs > 30000 {
		return fmt.Errorf("timeout_ms must be 0-30000 (0 = 3s default)")
	}
	return nil
}

// QRPayload builds the installer-facing claim payload encoded into the QR
// code the dashboard shows. Scanning it opens the edge claim flow with the
// one-time code, serial, and API base URL prefilled.
func QRPayload(apiBase, code, serial string) string {
	q := url.Values{}
	q.Set("api", apiBase)
	q.Set("serial", serial)
	q.Set("code", code)
	return "hexmon://claim?" + q.Encode()
}

// Facts are the evidence inputs for step derivation.
type Facts struct {
	Claimed   bool  // gateway redeemed its claim code
	HasDevice bool  // a device profile was assigned
	TestOK    *bool // port-test outcome, nil = never run
	Live      bool  // first telemetry seen for the device
}

// DeriveState reports the wizard step the installer is on, from evidence.
// Order matters: later evidence wins, so a live device reports "live" even
// if the port test was skipped.
func DeriveState(f Facts) string {
	switch {
	case f.Live:
		return "live"
	case f.TestOK != nil && *f.TestOK:
		return "tested"
	case f.TestOK != nil:
		return "failed"
	case f.HasDevice:
		return "profiled"
	case f.Claimed:
		return "claimed"
	default:
		return "awaiting_claim"
	}
}
