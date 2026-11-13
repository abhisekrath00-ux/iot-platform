package main

import (
	"fmt"
	"math"
	"strings"
)

// Shared API checks complement the form; they do not prove a vendor map.
// Optional scale/range fields remain optional for legacy serial Modbus profiles.
func profileShapeGuards(driver string, pts []map[string]any) error {
	if len(pts) == 0 || len(pts) > 64 {
		return fmt.Errorf("1-64 points required")
	}
	ids := map[string]bool{}
	type span struct {
		start, end, fn int
		id             string
	}
	var spans []span
	finite := func(v any) (float64, bool) { n, ok := v.(float64); return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0) }
	for _, p := range pts {
		id, _ := p["id"].(string)
		if ids[id] {
			return fmt.Errorf("duplicate point id %q", id)
		}
		ids[id] = true
		if v, exists := p["scale"]; exists {
			n, ok := finite(v)
			if !ok || n == 0 {
				return fmt.Errorf("point %s: scale must be finite and nonzero", id)
			}
		}
		for _, k := range []string{"min", "max"} {
			if v, exists := p[k]; exists {
				if _, ok := finite(v); !ok {
					return fmt.Errorf("point %s: %s must be finite", id, k)
				}
			}
		}
		lo, hasLo := p["min"]
		hi, hasHi := p["max"]
		if hasLo && hasHi {
			a, _ := finite(lo)
			b, _ := finite(hi)
			if b <= a {
				return fmt.Errorf("point %s: max must exceed min", id)
			}
		}
		if !strings.HasPrefix(driver, "modbus") {
			continue
		}
		reg, ok := finite(p["register"])
		if !ok || reg != math.Trunc(reg) || reg < 0 || reg > 65535 {
			return fmt.Errorf("point %s: register must be a whole number 0-65535", id)
		}
		fn := 4.0
		if v, exists := p["func"]; exists {
			fn, ok = finite(v)
			if !ok || fn != math.Trunc(fn) || fn < 1 || fn > 4 {
				return fmt.Errorf("point %s: func must be a whole number 1-4", id)
			}
		}
		typ, _ := p["type"].(string)
		if typ == "" {
			if fn <= 2 {
				typ = "bool"
			} else {
				typ = "u16"
			}
		}
		width := 1
		if typ == "u32" || typ == "i32" || typ == "f32" {
			width = 2
		}
		if (fn <= 2 && typ != "bool") || (fn > 2 && typ == "bool") {
			return fmt.Errorf("point %s: bool is required for coils/discrete and not valid for register functions", id)
		}
		end := int(reg) + width - 1
		if end > 65535 {
			return fmt.Errorf("point %s: register width exceeds 65535", id)
		}
		for _, s := range spans {
			if s.fn == int(fn) && int(reg) <= s.end && end >= s.start {
				return fmt.Errorf("point %s: registers overlap %s in function %d", id, s.id, int(fn))
			}
		}
		spans = append(spans, span{int(reg), end, int(fn), id})
	}
	return nil
}

// Templates may save serial framing, never a guessed port or slave address.
func validateProfileDefaults(driver string, defaults map[string]any) error {
	if len(defaults) == 0 {
		return nil
	}
	if driver != "modbus-generic" && driver != "modbus-energy-meter" {
		return fmt.Errorf("connection defaults only supported for serial Modbus")
	}
	for k, v := range defaults {
		switch k {
		case "baud":
			n, ok := v.(float64)
			if !ok || !containsBaud(int(n)) || n != math.Trunc(n) {
				return fmt.Errorf("invalid default baud")
			}
		case "data_bits":
			if v != float64(8) {
				return fmt.Errorf("default data_bits must be 8")
			}
		case "stop_bits":
			if v != float64(1) && v != float64(2) {
				return fmt.Errorf("invalid default stop_bits")
			}
		case "parity":
			if v != "even" && v != "odd" && v != "none" {
				return fmt.Errorf("invalid default parity")
			}
		default:
			return fmt.Errorf("unsupported connection default %s", k)
		}
	}
	return nil
}
func containsBaud(n int) bool {
	for _, b := range []int{1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200} {
		if n == b {
			return true
		}
	}
	return false
}
