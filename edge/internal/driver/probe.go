// probe.go: one-off read-only diagnostic probes for guided commissioning.
// A probe builds a temporary modbus-generic device from the installer's
// parameters, reads once, and reports values or the exact failure. Probes
// never write registers: the commissioning API validates func 1-4 and the
// generic driver only ever issues read functions.
package driver

import (
	"context"
	"fmt"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// ProbeRequest mirrors the commissioning API's validated probe payload.
type ProbeRequest struct {
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

// ProbeReading is one decoded value from the probe.
type ProbeReading struct {
	PointID string  `json:"point_id"`
	Value   float64 `json:"value"`
}

// ProbeResult goes back to the control plane on the diag result topic.
type ProbeResult struct {
	SessionID  string         `json:"session_id"`
	OK         bool           `json:"ok"`
	Error      string         `json:"error,omitempty"`
	Readings   []ProbeReading `json:"readings,omitempty"`
	LatencyMs  int64          `json:"latency_ms"`
	FinishedAt time.Time      `json:"finished_at"`
}

// RunProbe executes one read-only probe. open is injectable for tests; pass
// nil in production to use the real serial port.
func RunProbe(ctx context.Context, open PortOpener, req ProbeRequest) ProbeResult {
	if open == nil {
		open = serial.Open
	}
	start := time.Now()
	res := ProbeResult{SessionID: req.SessionID}
	fail := func(err error) ProbeResult {
		res.Error = err.Error()
		res.LatencyMs = time.Since(start).Milliseconds()
		res.FinishedAt = time.Now().UTC()
		return res
	}

	// Defensive re-clamp: the API validates, the edge never trusts the wire.
	if req.Func < 1 || req.Func > 4 || req.Count < 1 || req.Count > 32 || req.Address < 1 || req.Address > 247 {
		return fail(fmt.Errorf("probe parameters out of diagnostic envelope"))
	}

	step := 1
	switch req.Type {
	case "u32", "i32", "f32":
		step = 2
	}
	pts := []config.Point{}
	for i := 0; i < req.Count; i += step {
		pts = append(pts, config.Point{
			ID: fmt.Sprintf("r%d", req.Register+i), Register: req.Register + i,
			Func: req.Func, Type: req.Type, WordOrder: req.WordOrder,
			Min: -1e18, Max: 1e18, // probe reports raw values, no range rejection
		})
	}
	dev := config.Device{
		ID: "diag-probe", Profile: "modbus-generic", Port: req.Port,
		Baud: req.Baud, DataBits: req.DataBits, StopBits: req.StopBits,
		Parity: req.Parity, Address: req.Address, Points: pts,
	}
	d, err := NewWithOpener(open, dev)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	readings, err := d.Poll(ctx)
	if err != nil {
		return fail(err)
	}
	for _, r := range readings {
		res.Readings = append(res.Readings, ProbeReading{PointID: r.PointID, Value: r.Value})
	}
	res.OK = true
	res.LatencyMs = time.Since(start).Milliseconds()
	res.FinishedAt = time.Now().UTC()
	return res
}
