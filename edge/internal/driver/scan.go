package driver

import (
	"context"
	"fmt"
)

// ScanRequest asks for a read-only sweep of Modbus RTU slave addresses on one
// port: one single-register read (function 3 or 4) per address. Nothing is
// ever written. A device is reported when it answers that read correctly.
type ScanRequest struct {
	Port     string `json:"port"`
	Baud     int    `json:"baud"`
	DataBits int    `json:"data_bits"`
	StopBits int    `json:"stop_bits"`
	Parity   string `json:"parity"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Func     int    `json:"func"`     // 3 or 4 (default 4)
	Register int    `json:"register"` // a register that exists on the device model (default 0)
}

// ScanFound is one responding slave.
type ScanFound struct {
	Address int `json:"address"`
	Value   int `json:"value"`
}

// ScanModbusRTU sweeps From..To (max 247 addresses, 1..247). It stops early
// when ctx is cancelled and returns what it found so far. The register must be
// one the device implements: a slave that exists but rejects it with an
// exception is not reported, so try the model's documented first register.
func ScanModbusRTU(ctx context.Context, open PortOpener, req ScanRequest) ([]ScanFound, error) {
	if req.Func == 0 {
		req.Func = 4
	}
	if req.Func != 3 && req.Func != 4 {
		return nil, fmt.Errorf("scan uses function 3 or 4 only")
	}
	if req.From < 1 || req.To > 247 || req.From > req.To {
		return nil, fmt.Errorf("scan range must be within 1-247")
	}
	var found []ScanFound
	for a := req.From; a <= req.To; a++ {
		if err := ctx.Err(); err != nil {
			return found, err
		}
		res := RunProbe(ctx, open, ProbeRequest{
			Port: req.Port, Baud: req.Baud, DataBits: req.DataBits, StopBits: req.StopBits, Parity: req.Parity,
			Address: a, Func: req.Func, Register: req.Register, Count: 1, Type: "u16", WordOrder: "abcd",
		})
		if res.OK && len(res.Readings) == 1 {
			found = append(found, ScanFound{Address: a, Value: int(res.Readings[0].Value)})
		}
	}
	return found, nil
}
