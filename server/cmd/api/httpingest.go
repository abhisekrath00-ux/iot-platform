package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
)

// maxIngestBatch bounds one HTTP push; larger batches get 413 so a client
// cannot hold a connection and a transaction-sized write open (backpressure).
const maxIngestBatch = 500

type ingestReading struct {
	Point   string   `json:"point"`
	Value   *float64 `json:"value"`
	Unit    string   `json:"unit"`
	TS      string   `json:"ts"`
	EventID string   `json:"event_id"`
}

// ingestHTTP lets any HTTP-capable device or bridge (ESP32, STM32 with a modem,
// PLC gateways, third-party systems) push readings for a device that was
// onboarded in the UI. Same validation as the MQTT path: the device must belong
// to the caller's tenant, the point must be registered, values are range
// checked against the point's min/max, and event ids make retries idempotent.
// Unscoped keys and users need operator; scoped keys need the "telemetry" scope.
func (s *server) ingestHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		DeviceID string          `json:"device_id"`
		Readings []ingestReading `json:"readings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.DeviceID == "" {
		http.Error(w, "bad body: device_id and readings required", 400)
		return
	}
	if len(in.Readings) == 0 {
		http.Error(w, "no readings", 400)
		return
	}
	if len(in.Readings) > maxIngestBatch {
		http.Error(w, fmt.Sprintf("batch too large (max %d)", maxIngestBatch), http.StatusRequestEntityTooLarge)
		return
	}
	ctx, tenant := r.Context(), auth.Tenant(r)
	var gw string
	if err := s.st.Pool.QueryRow(ctx, `SELECT gateway_id FROM devices WHERE id=$1 AND tenant_id=$2`, in.DeviceID, tenant).Scan(&gw); err != nil {
		http.Error(w, "unknown device", 404)
		return
	}
	type pt struct {
		unit     string
		min, max *float64
	}
	pts := map[string]pt{}
	rows, err := s.st.Pool.Query(ctx, `SELECT id, unit, min_value, max_value FROM points WHERE device_id=$1`, in.DeviceID)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	for rows.Next() {
		var id string
		var p pt
		if rows.Scan(&id, &p.unit, &p.min, &p.max) == nil {
			pts[id] = p
		}
	}
	rows.Close()

	now := time.Now()
	type rej struct {
		Index  int    `json:"index"`
		Reason string `json:"reason"`
	}
	accepted, rejected := 0, []rej{}
	n := notify.FromEnv()
	type okRow struct {
		idx int
		t   store.Telemetry
	}
	var good []okRow
	for i, rd := range in.Readings {
		p, ok := pts[rd.Point]
		switch {
		case !ok:
			rejected = append(rejected, rej{i, "unknown point"})
			continue
		case rd.Value == nil || math.IsNaN(*rd.Value) || math.IsInf(*rd.Value, 0):
			rejected = append(rejected, rej{i, "missing or non-finite value"})
			continue
		case (p.min != nil && *rd.Value < *p.min) || (p.max != nil && *rd.Value > *p.max):
			rejected = append(rejected, rej{i, "out of range"})
			continue
		}
		at := now
		if rd.TS != "" {
			t, err := time.Parse(time.RFC3339, rd.TS)
			if err != nil || t.After(now.Add(5*time.Minute)) || now.Sub(t) > 30*24*time.Hour {
				rejected = append(rejected, rej{i, "implausible ts"})
				continue
			}
			at = t
		}
		eid := rd.EventID
		if eid == "" {
			h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%v", tenant, in.DeviceID, rd.Point, at.UnixNano(), *rd.Value)))
			eid = "http-" + hex.EncodeToString(h[:12])
		} else if len(eid) > 128 {
			rejected = append(rejected, rej{i, "event_id too long"})
			continue
		}
		if rd.EventID != "" && rd.TS == "" {
			// A retry without ts must reuse the first attempt's time or the
			// (event_id, observed_at) dedupe key would not match.
			var prev time.Time
			if s.st.Pool.QueryRow(ctx, `SELECT observed_at FROM telemetry WHERE tenant_id=$1 AND event_id=$2 LIMIT 1`, tenant, eid).Scan(&prev) == nil {
				at = prev
			}
		}
		unit := rd.Unit
		if unit == "" {
			unit = p.unit
		}
		good = append(good, okRow{i, store.Telemetry{EventID: eid, TenantID: tenant, GatewayID: gw, DeviceID: in.DeviceID,
			PointID: rd.Point, ObservedAt: at, Value: *rd.Value, Unit: unit, Quality: "measured", SchemaVersion: 1}})
	}
	// One round trip for the whole batch instead of one per reading.
	batch := &pgx.Batch{}
	for _, g := range good {
		e := g.t
		batch.Queue(`INSERT INTO telemetry
			(event_id,tenant_id,site_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
			VALUES ($1,$2,NULL,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (event_id, observed_at) DO NOTHING`,
			e.EventID, e.TenantID, e.GatewayID, e.DeviceID, e.PointID, e.ObservedAt, e.Value, e.Unit, e.Quality, e.SchemaVersion)
	}
	br := s.st.Pool.SendBatch(ctx, batch)
	for _, g := range good {
		if _, err := br.Exec(); err != nil {
			rejected = append(rejected, rej{g.idx, "store error"})
			continue
		}
		accepted++
		rules.Evaluate(ctx, s.st.Pool, n, tenant, in.DeviceID, g.t.PointID, g.t.Value)
		flow.EvaluateWith(ctx, s.st.Pool, n, fnRunner, tenant, in.DeviceID, g.t.PointID, g.t.Value)
	}
	br.Close()
	code := 200
	if accepted == 0 {
		code = 422
	}
	writeJSON(w, code, map[string]any{"accepted": accepted, "rejected": rejected})
}
