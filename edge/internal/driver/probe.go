// probe.go: one-off read-only diagnostic probes for guided commissioning.
// A probe builds a temporary modbus-generic device from the installer's
// parameters, reads once, and reports values or the exact failure. Probes
// never write registers: the commissioning API validates func 1-4 and the
// generic driver only ever issues read functions.
package driver

import (
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// ProbeRequest mirrors the commissioning API's validated probe payload.
type ProbeRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id,omitempty"`
	Transport string `json:"transport,omitempty"`
	Host      string `json:"host,omitempty"`
	NetPort   int    `json:"net_port,omitempty"`
	RawRange  bool   `json:"raw_range,omitempty"`
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
	RequestID  string         `json:"request_id,omitempty"`
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
	res := ProbeResult{SessionID: req.SessionID, RequestID: req.RequestID}
	fail := func(err error) ProbeResult {
		res.Error = err.Error()
		res.LatencyMs = time.Since(start).Milliseconds()
		res.FinishedAt = time.Now().UTC()
		return res
	}

	// Defensive re-clamp: the API validates, the edge never trusts the wire.
	if req.Func < 1 || req.Func > 4 || req.Count < 1 || req.Count > 32 || req.Address < 1 || req.Address > 247 || req.Register < 0 || req.Register+req.Count > 65536 || (req.Func <= 2 && req.Type != "bool") || (req.Func > 2 && req.Type == "bool") {
		return fail(fmt.Errorf("probe parameters out of diagnostic envelope"))
	}

	step := 1
	switch req.Type {
	case "u32", "i32", "f32", "t5", "t6":
		step = 2
	}
	if req.Count%step != 0 {
		return fail(fmt.Errorf("probe count must contain whole decoded values"))
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
	if req.Transport != "" && req.Transport != "rtu" && req.Transport != "tcp" {
		return fail(fmt.Errorf("unsupported probe transport"))
	}
	if req.Transport == "tcp" {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$`).MatchString(req.Host) || req.NetPort < 1 || req.NetPort > 65535 {
			return fail(fmt.Errorf("invalid TCP target"))
		}
		dev.Profile = "modbus-tcp"
		dev.Host = req.Host
		dev.NetPort = req.NetPort
	}
	d, err := NewWithOpener(open, dev)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	if req.RawRange {
		if (req.Func <= 2 && req.Type != "bool") || (req.Func > 2 && req.Type != "u16") || req.WordOrder != "abcd" {
			return fail(fmt.Errorf("raw range requires bool/u16 wire words"))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		g, ok := d.(*modbusGeneric)
		if !ok {
			return fail(fmt.Errorf("unsupported raw probe driver"))
		}
		if g.read == nil {
			g.read = g.readRegisters
		}
		data, err := g.read(req.Func, req.Register, req.Count)
		if err != nil {
			return fail(err)
		}
		need := req.Count * 2
		if req.Func <= 2 {
			need = (req.Count + 7) / 8
		}
		if len(data) != need {
			return fail(fmt.Errorf("incomplete raw range response"))
		}
		for i := 0; i < req.Count; i++ {
			value := float64(0)
			if req.Func <= 2 {
				value = float64((data[i/8] >> uint(i%8)) & 1)
			} else {
				value = float64(binary.BigEndian.Uint16(data[2*i:]))
			}
			res.Readings = append(res.Readings, ProbeReading{PointID: fmt.Sprintf("r%d", req.Register+i), Value: value})
		}
		res.OK = true
		res.LatencyMs = time.Since(start).Milliseconds()
		res.FinishedAt = time.Now().UTC()
		return res
	}
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
