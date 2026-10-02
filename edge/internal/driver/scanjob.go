package driver

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"go.bug.st/serial"
)

// Server-requested Modbus RTU scan: sweep slave addresses read-only, then for
// each responder read the first few points of every candidate profile the
// server sent and score how plausible each profile is. A match means "the
// registers answered with values inside the profile's validation ranges". It
// is a suggestion for a human to confirm, not proof of the device model.

// CandPoint is one register of a candidate profile.
type CandPoint struct {
	ID        string  `json:"id"`
	Register  int     `json:"register"`
	Func      int     `json:"func"`
	Type      string  `json:"type"`
	WordOrder string  `json:"word_order"`
	Scale     float64 `json:"scale"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
}

// Candidate is a tenant profile offered for matching (first points only).
type Candidate struct {
	ProfileID string      `json:"profile_id"`
	Name      string      `json:"name"`
	Points    []CandPoint `json:"points"`
}

type SweepRequest struct {
	ScanID     string      `json:"scan_id"`
	Port       string      `json:"port"`
	Baud       int         `json:"baud"`
	DataBits   int         `json:"data_bits"`
	StopBits   int         `json:"stop_bits"`
	Parity     string      `json:"parity"`
	From       int         `json:"from"`
	To         int         `json:"to"`
	TimeoutMs  int         `json:"timeout_ms"`
	Candidates []Candidate `json:"candidates"`
}

type Match struct {
	ProfileID string  `json:"profile_id"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"` // fraction of probed points inside the profile's range
	Probed    int     `json:"probed"`
}

type Slave struct {
	Address int     `json:"address"`
	Matches []Match `json:"matches"`
}

type SweepResult struct {
	Slaves []Slave `json:"slaves"`
	Error  string  `json:"error,omitempty"`
	Note   string  `json:"note,omitempty"`
}

const (
	maxSweepCandidates = 20
	maxProbePoints     = 3
)

type fc struct{ fn, reg int }

// SweepModbusRTU runs the sweep. It only issues Modbus read functions.
func SweepModbusRTU(ctx context.Context, open PortOpener, req SweepRequest) SweepResult {
	var res SweepResult
	if open == nil {
		open = serial.Open
	}
	if req.From < 1 || req.To > 247 || req.From > req.To {
		res.Error = "scan range must be within 1-247"
		return res
	}
	if len(req.Candidates) > maxSweepCandidates {
		req.Candidates = req.Candidates[:maxSweepCandidates]
	}
	to := req.TimeoutMs
	if to < 100 || to > 3000 {
		to = 400
	}
	// Fail loudly if the port cannot be opened (most often another process, such
	// as this agent's own poller, holds it).
	p, err := open(req.Port, &serial.Mode{BaudRate: req.Baud, DataBits: req.DataBits, StopBits: serial.StopBits(req.StopBits)})
	if err != nil {
		res.Error = fmt.Sprintf("cannot open %s: %v (is a polling device already using this port?)", req.Port, err)
		return res
	}
	p.Close()

	// Sweep registers: the first point of each candidate, plus the common starts.
	pairs := []fc{}
	seen := map[fc]bool{}
	add := func(f fc) {
		if !seen[f] && len(pairs) < 5 {
			seen[f] = true
			pairs = append(pairs, f)
		}
	}
	for _, c := range req.Candidates {
		if len(c.Points) > 0 && c.Points[0].Func >= 3 && c.Points[0].Func <= 4 {
			add(fc{c.Points[0].Func, c.Points[0].Register})
		}
	}
	add(fc{3, 0})
	add(fc{4, 0})

	probe := func(addr int, f fc, typ, wo string, count int) ProbeResult {
		pctx, cancel := context.WithTimeout(ctx, time.Duration(to)*time.Millisecond*2)
		defer cancel()
		return RunProbe(pctx, open, ProbeRequest{Port: req.Port, Baud: req.Baud, DataBits: req.DataBits, StopBits: req.StopBits, Parity: req.Parity,
			Address: addr, Func: f.fn, Register: f.reg, Count: count, Type: typ, WordOrder: wo, TimeoutMs: to})
	}
	for a := req.From; a <= req.To; a++ {
		if ctx.Err() != nil {
			res.Note = "stopped early: " + ctx.Err().Error()
			break
		}
		answered := false
		for _, f := range pairs {
			if r := probe(a, f, "u16", "abcd", 1); r.OK {
				answered = true
				break
			}
		}
		if !answered {
			continue
		}
		s := Slave{Address: a, Matches: []Match{}}
		for _, c := range req.Candidates {
			n, good := 0, 0
			for i, pt := range c.Points {
				if i >= maxProbePoints {
					break
				}
				if pt.Func < 1 || pt.Func > 4 || pt.Register < 0 || pt.Register > 65535 {
					continue
				}
				cnt := 1
				if pt.Type == "u32" || pt.Type == "i32" || pt.Type == "f32" {
					cnt = 2
				}
				n++
				r := probe(a, fc{pt.Func, pt.Register}, pt.Type, pt.WordOrder, cnt)
				if !r.OK || len(r.Readings) == 0 {
					continue
				}
				v := r.Readings[0].Value
				if pt.Scale > 0 {
					v *= pt.Scale
				}
				if !math.IsNaN(v) && v >= pt.Min && v <= pt.Max {
					good++
				}
			}
			if n > 0 && good > 0 {
				s.Matches = append(s.Matches, Match{ProfileID: c.ProfileID, Name: c.Name, Score: float64(good) / float64(n), Probed: n})
			}
		}
		sort.SliceStable(s.Matches, func(i, j int) bool { return s.Matches[i].Score > s.Matches[j].Score })
		res.Slaves = append(res.Slaves, s)
	}
	return res
}
