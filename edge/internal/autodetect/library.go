// Package autodetect lets the edge agent find devices on its own, read-only,
// and keep a local list of proposals. It needs no server: profiles for matching
// are built in (plus optional YAML files and a cache of the tenant's profiles
// from earlier server scans). See docs/edge-autodetect.md.
package autodetect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

// Profile is a device model the edge can recognise. Match holds the first
// registers used to score a responding slave. Points is the full polling
// definition; a profile without Points can be suggested but never auto-added.
type Profile struct {
	ID       string             `yaml:"id" json:"id"`
	Name     string             `yaml:"name" json:"name"`
	Interval time.Duration      `yaml:"interval" json:"-"`
	Match    []driver.CandPoint `yaml:"match" json:"-"`
	Points   []config.Point     `yaml:"points" json:"-"`
	Source   string             `yaml:"-" json:"source"` // builtin | file | server-cache
}

func f32(id string, reg int, unit string, min, max float64) config.Point {
	return config.Point{ID: id, Register: reg, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: unit, Min: min, Max: max}
}

func cp(id string, reg int, min, max float64) driver.CandPoint {
	return driver.CandPoint{ID: id, Register: reg, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Min: min, Max: max}
}

// Builtin profiles. Register maps come from the manufacturer documents in
// docs/devices/. The match ranges are deliberately tighter than the polling
// ranges (a mains voltage is 80-500 V, not 0-600000) so a slave that happens
// to answer zeros is not mistaken for a meter. Never run against real hardware.
func Builtin() []Profile {
	return []Profile{
		{
			ID: "builtin:selec-mx300", Name: "Selec MX300-1-C-CE (single-phase meter)", Interval: 10 * time.Second, Source: "builtin",
			Match: []driver.CandPoint{cp("voltage", 0, 80, 500), cp("current", 2, 0, 10000), cp("frequency", 12, 40, 70)},
			Points: []config.Point{
				f32("voltage", 0x00, "V", 0, 600000), f32("current", 0x02, "A", 0, 10000), f32("active_power", 0x04, "kW", -1e6, 1e6),
				f32("reactive_power", 0x06, "kvar", -1e6, 1e6), f32("apparent_power", 0x08, "kVA", 0, 1e6),
				f32("power_factor", 0x0A, "", -1, 1), f32("frequency", 0x0C, "Hz", 0, 65),
			},
		},
		{
			ID: "builtin:selec-mfm383a", Name: "Selec MFM383A-C (three-phase meter)", Interval: 10 * time.Second, Source: "builtin",
			Match: []driver.CandPoint{cp("v1n", 0, 80, 500), cp("v2n", 2, 0, 500), cp("freq", 56, 40, 70)},
			Points: []config.Point{
				f32("v1n", 0, "V", 0, 100000), f32("v2n", 2, "V", 0, 100000), f32("v3n", 4, "V", 0, 100000),
				f32("i1", 16, "A", 0, 100000), f32("i2", 18, "A", 0, 100000), f32("i3", 20, "A", 0, 100000),
				f32("kw_total", 42, "kW", -1e7, 1e7), f32("pf_avg", 54, "", -1, 1), f32("freq", 56, "Hz", 0, 65),
				f32("kwh", 58, "kWh", 0, 1e12),
			},
		},
	}
}

// LoadLibrary returns the built-ins, then *.yaml files in dir (each file is one
// Profile; a file with the same id replaces the built-in), then the cached
// server candidates (match-only, never auto-added).
func LoadLibrary(dir string, cached []driver.Candidate) ([]Profile, []string) {
	var warns []string
	byID := map[string]Profile{}
	for _, p := range Builtin() {
		byID[p.ID] = p
	}
	if dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				warns = append(warns, f+": "+err.Error())
				continue
			}
			var p Profile
			if err := yaml.Unmarshal(b, &p); err != nil || p.ID == "" || len(p.Match) == 0 {
				warns = append(warns, f+": needs id and match points")
				continue
			}
			if p.Interval <= 0 {
				p.Interval = 10 * time.Second
			}
			for _, pt := range p.Points {
				if pt.Max <= pt.Min {
					warns = append(warns, f+": point "+pt.ID+" needs a validation range")
					p.Points = nil
					break
				}
			}
			p.Source = "file"
			byID[p.ID] = p
		}
	}
	for _, c := range cached {
		if c.ProfileID == "" || len(c.Points) == 0 {
			continue
		}
		if _, ok := byID[c.ProfileID]; ok {
			continue
		}
		byID[c.ProfileID] = Profile{ID: c.ProfileID, Name: c.Name, Match: c.Points, Source: "server-cache"}
	}
	out := make([]Profile, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, warns
}

// Candidates converts profiles to the matching form the Modbus sweep uses.
func Candidates(ps []Profile) []driver.Candidate {
	out := make([]driver.Candidate, 0, len(ps))
	for _, p := range ps {
		pts := p.Match
		if len(pts) > 3 {
			pts = pts[:3]
		}
		out = append(out, driver.Candidate{ProfileID: p.ID, Name: p.Name, Points: pts})
	}
	return out
}

// SaveCandidates caches the profiles a server scan request carried, so the edge
// can keep matching against them with no server. Atomic, best effort.
func SaveCandidates(path string, cs []driver.Candidate) {
	if len(cs) == 0 {
		return
	}
	b, err := json.Marshal(cs)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

// LoadCandidates reads that cache; a missing or corrupt file gives nothing.
func LoadCandidates(path string) []driver.Candidate {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cs []driver.Candidate
	if json.Unmarshal(b, &cs) != nil {
		return nil
	}
	return cs
}
