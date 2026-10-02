package main

import (
	"context"
	"encoding/json"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/autodetect"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

// Dashboard-requested discovery scans. The server sends a validated request on
// t/<tenant>/g/<gw>/scan; the agent re-validates, runs one scan at a time with a
// hard time limit, and answers on scan/result. Scans only read: Modbus read
// functions, TCP connects, a BACnet Who-Is. Nothing is added or changed on the
// gateway; the operator adds devices from the results in the dashboard.

type scanRequest struct {
	ScanID string `json:"scan_id"`
	Kind   string `json:"kind"` // modbus-rtu | lan | bacnet
	// modbus-rtu
	driver.SweepRequest
	// lan
	CIDR string `json:"cidr"`
	// bacnet
	Broadcast string `json:"broadcast"`
}

type scanResult struct {
	ScanID     string                  `json:"scan_id"`
	Kind       string                  `json:"kind"`
	OK         bool                    `json:"ok"`
	Error      string                  `json:"error,omitempty"`
	Note       string                  `json:"note,omitempty"`
	Slaves     []driver.Slave          `json:"slaves,omitempty"`
	Hosts      []discover.Hit          `json:"hosts,omitempty"`
	BACnet     []discover.BACnetDevice `json:"bacnet,omitempty"`
	FinishedAt time.Time               `json:"finished_at"`
}

var scanMu sync.Mutex

const scanLimit = 8 * time.Minute

// runScanRequest handles one request payload and returns the result to publish.
func runScanRequest(ctx context.Context, payload []byte, open driver.PortOpener) scanResult {
	var req scanRequest
	res := scanResult{}
	fail := func(msg string) scanResult {
		res.Error = msg
		res.FinishedAt = time.Now().UTC()
		return res
	}
	if err := json.Unmarshal(payload, &req); err != nil || req.ScanID == "" {
		return fail("bad scan request")
	}
	res.ScanID, res.Kind = req.ScanID, req.Kind
	if !scanMu.TryLock() {
		return fail("another scan is already running on this gateway")
	}
	defer scanMu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, scanLimit)
	defer cancel()
	switch req.Kind {
	case "modbus-rtu":
		r := req.SweepRequest
		autodetect.SaveCandidates(candidatesPath(), r.Candidates) // lets the edge keep matching offline
		sr := driver.SweepModbusRTU(sctx, open, r)
		res.Slaves, res.Note = sr.Slaves, sr.Note
		if sr.Error != "" {
			return fail(sr.Error)
		}
	case "lan":
		hits, err := discover.ScanLAN(sctx, req.CIDR, 400*time.Millisecond)
		if err != nil {
			return fail(err.Error())
		}
		res.Hosts = hits
	case "bacnet":
		ds, err := discover.FindBACnet(sctx, req.Broadcast, 3*time.Second)
		if err != nil {
			return fail(err.Error())
		}
		res.BACnet = ds
	default:
		return fail("unknown scan kind")
	}
	res.OK = true
	res.FinishedAt = time.Now().UTC()
	return res
}
