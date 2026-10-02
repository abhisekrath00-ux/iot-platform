// Package rules evaluates enabled v1 threshold rules against incoming
// telemetry and raises alerts with notification dispatch.
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/forecast"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/kpi"
)

type Definition struct {
	DeviceID string `json:"device_id"` // empty = any device exposing the point
	// Profile limits the rule to devices of one device profile (asset type), so
	// one rule covers every device of that type, now and in the future.
	Profile   string  `json:"profile,omitempty"`
	PointID   string  `json:"point_id"`
	Op        string  `json:"op"` // ">" or "<"
	Threshold float64 `json:"threshold"`
	Severity  string  `json:"severity"` // info|warning|critical
	Message   string  `json:"message"`

	// Kind selects the rule type: "" or "threshold" (Op and Threshold),
	// "sigma" (deviation from the point's own recent history) or "kpi_band"
	// (a KPI leaving a band). Sigma and band rules are off until created.
	Kind string `json:"kind,omitempty"`
	// sigma
	Sigma         float64 `json:"sigma,omitempty"`          // 2 to 10 standard deviations
	WindowMinutes int     `json:"window_minutes,omitempty"` // history used as the baseline, 10 to 10080
	Direction     string  `json:"direction,omitempty"`      // both (default) | above | below
	// seasonal: like sigma, but the baseline is the same hour of the day (or
	// hour of the week) over the last WindowDays days, so a daily peak is not
	// an anomaly. Uses Sigma and Direction. Statistical, not learned.
	WindowDays int    `json:"window_days,omitempty"` // 3 to 28
	Season     string `json:"season,omitempty"`      // hour_of_day (default) | hour_of_week
	// forecast_limit: fire when the statistical forecast crosses Threshold (in
	// direction Op) within HorizonHours. Needs DeviceID and PointID, and only
	// fires when the model beat repeating yesterday in its backtest.
	HorizonHours int `json:"horizon_hours,omitempty"` // 1 to 72
	// kpi_band
	KPIID string   `json:"kpi_id,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
}

// MinBaselineSamples is the history a sigma rule needs before it may fire.
const MinBaselineSamples = 30

// Validate checks a rule definition. The API calls it before storing a rule.
func (d Definition) Validate() error {
	if d.Severity != "info" && d.Severity != "warning" && d.Severity != "critical" {
		return fmt.Errorf("severity must be info, warning or critical")
	}
	if len(d.Profile) > 64 || len(d.Message) > 500 {
		return fmt.Errorf("profile or message too long")
	}
	switch d.Kind {
	case "", "threshold":
		if d.PointID == "" || (d.Op != ">" && d.Op != "<") {
			return fmt.Errorf("threshold rule needs point_id and op > or <")
		}
	case "sigma":
		if d.PointID == "" {
			return fmt.Errorf("sigma rule needs point_id")
		}
		if d.Sigma < 2 || d.Sigma > 10 {
			return fmt.Errorf("sigma must be between 2 and 10")
		}
		if d.WindowMinutes < 10 || d.WindowMinutes > 10080 {
			return fmt.Errorf("window_minutes must be between 10 and 10080")
		}
		if d.Direction != "" && d.Direction != "both" && d.Direction != "above" && d.Direction != "below" {
			return fmt.Errorf("direction must be both, above or below")
		}
	case "seasonal":
		if d.PointID == "" {
			return fmt.Errorf("seasonal rule needs point_id")
		}
		if d.Sigma < 2 || d.Sigma > 10 {
			return fmt.Errorf("sigma must be between 2 and 10")
		}
		if d.WindowDays < 3 || d.WindowDays > 28 {
			return fmt.Errorf("window_days must be between 3 and 28")
		}
		if d.Season != "" && d.Season != "hour_of_day" && d.Season != "hour_of_week" {
			return fmt.Errorf("season must be hour_of_day or hour_of_week")
		}
		if d.Direction != "" && d.Direction != "both" && d.Direction != "above" && d.Direction != "below" {
			return fmt.Errorf("direction must be both, above or below")
		}
	case "forecast_limit":
		if d.DeviceID == "" || d.PointID == "" || (d.Op != ">" && d.Op != "<") {
			return fmt.Errorf("forecast_limit rule needs device_id, point_id and op > or <")
		}
		if d.HorizonHours < 1 || d.HorizonHours > 72 {
			return fmt.Errorf("horizon_hours must be between 1 and 72")
		}
	case "kpi_band":
		if d.KPIID == "" {
			return fmt.Errorf("kpi_band rule needs kpi_id")
		}
		if d.Min == nil && d.Max == nil {
			return fmt.Errorf("kpi_band rule needs min, max or both")
		}
		if d.Min != nil && d.Max != nil && *d.Min >= *d.Max {
			return fmt.Errorf("min must be below max")
		}
	default:
		return fmt.Errorf("unknown rule kind %q", d.Kind)
	}
	return nil
}

// SigmaDeviation returns how many standard deviations value is from the mean
// of the baseline, signed. ok is false when the baseline is too small or has
// no spread (nothing can be called unusual against a flat line).
func SigmaDeviation(baseline []float64, value float64) (z, mean, std float64, ok bool) {
	if len(baseline) < MinBaselineSamples {
		return 0, 0, 0, false
	}
	for _, v := range baseline {
		mean += v
	}
	mean /= float64(len(baseline))
	for _, v := range baseline {
		std += (v - mean) * (v - mean)
	}
	std = math.Sqrt(std / float64(len(baseline)-1))
	if std < 1e-9 {
		return 0, mean, 0, false
	}
	return (value - mean) / std, mean, std, true
}

// BandBreach reports whether v is outside the band and which side.
func BandBreach(v float64, min, max *float64) (side string, out bool) {
	if min != nil && v < *min {
		return "below", true
	}
	if max != nil && v > *max {
		return "above", true
	}
	return "", false
}

type Notifier interface {
	Email(ctx context.Context, to []string, subject, body string) error
	Slack(ctx context.Context, channel, text string) error
}

// Evaluate checks tenant rules against one reading and raises alerts.
func Evaluate(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, deviceID, pointID string, value float64) {
	rows, err := pool.Query(ctx,
		`SELECT id, definition FROM rules WHERE tenant_id=$1 AND enabled`, tenantID)
	if err != nil {
		log.Printf("rules: load: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw json.RawMessage
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		var d Definition
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		if d.PointID != pointID || (d.DeviceID != "" && d.DeviceID != deviceID) {
			continue
		}
		if d.Profile != "" {
			var prof string
			if pool.QueryRow(ctx, `SELECT profile FROM devices WHERE id=$1 AND tenant_id=$2`, deviceID, tenantID).Scan(&prof) != nil || prof != d.Profile {
				continue
			}
		}
		switch d.Kind {
		case "kpi_band", "forecast_limit":
			continue // evaluated periodically (EvaluateKPIs, EvaluateForecasts), not per reading
		case "seasonal":
			if msg, hit := seasonalHit(ctx, pool, tenantID, d, deviceID, pointID, value); hit {
				d.Message = firstNonEmpty(d.Message, msg)
				fire(ctx, pool, n, tenantID, id, d, deviceID, pointID, value)
			}
			continue
		case "sigma":
			if msg, hit := sigmaHit(ctx, pool, tenantID, d, deviceID, pointID, value); hit {
				d.Message = firstNonEmpty(d.Message, msg)
				fire(ctx, pool, n, tenantID, id, d, deviceID, pointID, value)
			}
			continue
		}
		hit := (d.Op == ">" && value > d.Threshold) || (d.Op == "<" && value < d.Threshold)
		if !hit {
			continue
		}
		fire(ctx, pool, n, tenantID, id, d, deviceID, pointID, value)
	}
}

func fire(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, ruleID string, d Definition, deviceID, pointID string, value float64) {
	// one open alert per rule and device: dedupe
	var existing string
	err := pool.QueryRow(ctx,
		`SELECT id FROM alerts WHERE rule_id=$1 AND status='open' AND COALESCE(device_id,'')=$2 LIMIT 1`, ruleID, deviceID).Scan(&existing)
	if err == nil {
		return // already open
	}
	msg := d.Message
	if msg == "" {
		msg = fmt.Sprintf("%s/%s %s %v (value %.3g)", deviceID, pointID, d.Op, d.Threshold, value)
	}
	alertID := uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO alerts(id,tenant_id,rule_id,severity,message,device_id) VALUES($1,$2,$3,$4,$5,$6)`,
		alertID, tenantID, ruleID, d.Severity, msg, deviceID); err != nil {
		log.Printf("rules: insert alert: %v", err)
		return
	}
	dispatch(ctx, pool, n, tenantID, d.Severity, msg)
}

func dispatch(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, severity, msg string) {
	if n == nil {
		return
	}
	rows, err := pool.Query(ctx,
		`SELECT type, target FROM notification_channels WHERE tenant_id=$1 AND enabled`, tenantID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var typ, target string
		if err := rows.Scan(&typ, &target); err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		switch typ {
		case "email":
			err = n.Email(cctx, []string{target}, "[Hexmon IoT] "+severity+" alert", msg)
		case "slack":
			err = n.Slack(cctx, target, "["+severity+"] "+msg)
		case "webhook":
			if wh, ok := n.(interface {
				Webhook(context.Context, string, string, map[string]any) error
			}); ok {
				err = wh.Webhook(cctx, target, "alert.raised", map[string]any{"severity": severity, "message": msg})
			}
		}
		cancel()
		if err != nil {
			log.Printf("notify %s -> %s: %v", typ, target, err)
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sigmaHit compares the new reading with the point's measured history in the
// rule's window (the new reading is already stored, so it is excluded by time).
func sigmaHit(ctx context.Context, pool *pgxpool.Pool, tenantID string, d Definition, deviceID, pointID string, value float64) (string, bool) {
	rows, err := pool.Query(ctx,
		`SELECT value FROM telemetry
		 WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality='measured'
		   AND observed_at > now() - make_interval(mins => $4) AND observed_at < now() - interval '1 second'
		 ORDER BY observed_at DESC LIMIT 5000`, tenantID, deviceID, pointID, d.WindowMinutes)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var base []float64
	for rows.Next() {
		var v float64
		if rows.Scan(&v) == nil {
			base = append(base, v)
		}
	}
	z, mean, std, ok := SigmaDeviation(base, value)
	if !ok {
		return "", false
	}
	dir := d.Direction
	if dir == "" {
		dir = "both"
	}
	if !((dir != "below" && z >= d.Sigma) || (dir != "above" && z <= -d.Sigma)) {
		return "", false
	}
	side := "above"
	if z < 0 {
		side = "below"
	}
	return fmt.Sprintf("%s/%s value %.4g is %.1f standard deviations %s its %d-minute mean %.4g (std %.3g, %d samples)",
		deviceID, pointID, value, math.Abs(z), side, d.WindowMinutes, mean, std, len(base)), true
}

// SeasonalHit is the pure decision shared with tests: z-score of value against
// the same-season history, honouring direction.
func SeasonalHit(base []float64, value, sigma float64, direction string) (z float64, hit bool) {
	z, _, _, ok := SigmaDeviation(base, value)
	if !ok {
		return 0, false
	}
	if direction == "" {
		direction = "both"
	}
	return z, (direction != "below" && z >= sigma) || (direction != "above" && z <= -sigma)
}

func seasonalHit(ctx context.Context, pool *pgxpool.Pool, tenantID string, d Definition, deviceID, pointID string, value float64) (string, bool) {
	cond := `extract(hour from observed_at AT TIME ZONE 'UTC') = extract(hour from now() AT TIME ZONE 'UTC')`
	label := "hour of day"
	if d.Season == "hour_of_week" {
		cond += ` AND extract(dow from observed_at AT TIME ZONE 'UTC') = extract(dow from now() AT TIME ZONE 'UTC')`
		label = "hour of week"
	}
	rows, err := pool.Query(ctx,
		`SELECT value FROM telemetry
		 WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality='measured'
		   AND observed_at > now() - make_interval(days => $4) AND observed_at < date_trunc('hour', now())
		   AND `+cond+` ORDER BY observed_at DESC LIMIT 5000`, tenantID, deviceID, pointID, d.WindowDays)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var base []float64
	for rows.Next() {
		var v float64
		if rows.Scan(&v) == nil {
			base = append(base, v)
		}
	}
	z, hit := SeasonalHit(base, value, d.Sigma, d.Direction)
	if !hit {
		return "", false
	}
	return fmt.Sprintf("%s/%s value %.4g is %.1f standard deviations from its usual level for this %s (UTC) over %d days (%d samples)",
		deviceID, pointID, value, math.Abs(z), label, d.WindowDays, len(base)), true
}

// EvaluateKPIs checks every enabled kpi_band rule against the KPI's current
// value. Run it periodically from one replica. A stale or incomputable KPI
// never raises an alert: no data is not a breach.
func EvaluateKPIs(ctx context.Context, pool *pgxpool.Pool, n Notifier) {
	rows, err := pool.Query(ctx, `SELECT id, tenant_id, definition FROM rules WHERE enabled AND definition->>'kind'='kpi_band'`)
	if err != nil {
		log.Printf("rules: kpi load: %v", err)
		return
	}
	type item struct {
		id, tenant string
		d          Definition
	}
	var list []item
	for rows.Next() {
		var it item
		var raw json.RawMessage
		if rows.Scan(&it.id, &it.tenant, &raw) == nil && json.Unmarshal(raw, &it.d) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	for _, it := range list {
		var name, expr string
		if pool.QueryRow(ctx, `SELECT name, expression FROM kpis WHERE id=$1 AND tenant_id=$2`, it.d.KPIID, it.tenant).Scan(&name, &expr) != nil {
			continue
		}
		v, ok := kpiValue(ctx, pool, it.tenant, expr)
		if !ok {
			continue
		}
		side, out := BandBreach(v, it.d.Min, it.d.Max)
		if !out {
			continue
		}
		d := it.d
		d.Message = firstNonEmpty(d.Message, fmt.Sprintf("KPI %s is %.4g, %s its band", name, v, side))
		fire(ctx, pool, n, it.tenant, it.id, d, "", d.KPIID, v)
	}
}

// kpiStaleAfter mirrors the API's KPI freshness rule.
const kpiStaleAfter = 15 * time.Minute

func kpiValue(ctx context.Context, pool *pgxpool.Pool, tenant, expr string) (float64, bool) {
	e, err := kpi.Parse(expr)
	if err != nil {
		return 0, false
	}
	vals := map[string]float64{}
	for _, ref := range e.Refs {
		var v float64
		var at time.Time
		if pool.QueryRow(ctx, `SELECT value, observed_at FROM telemetry
			WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality='measured' AND observed_at > now() - interval '24 hours'
			ORDER BY observed_at DESC LIMIT 1`, tenant, ref.Device, ref.Point).Scan(&v, &at) != nil || time.Since(at) > kpiStaleAfter {
			return 0, false
		}
		vals[ref.String()] = v
	}
	v, err := e.Eval(vals)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ForecastCrossing is the pure decision: does the forecast cross the limit
// within the horizon, and was the model good enough to say so?
func ForecastCrossing(fc []forecast.Point, op string, limit float64, horizon int, useful bool) (step int, hit bool) {
	if !useful || horizon < 1 {
		return 0, false
	}
	if horizon < len(fc) {
		fc = fc[:horizon]
	}
	st := forecast.FirstCrossing(fc, limit, op == ">")
	return st, st > 0
}

// EvaluateForecasts checks every enabled forecast_limit rule. Run it every
// 15 minutes or so from one replica. Too little data, or a model that did not
// beat the seasonal-naive backtest, never raises an alert.
func EvaluateForecasts(ctx context.Context, pool *pgxpool.Pool, n Notifier) {
	rows, err := pool.Query(ctx, `SELECT id, tenant_id, definition FROM rules WHERE enabled AND definition->>'kind'='forecast_limit'`)
	if err != nil {
		log.Printf("rules: forecast load: %v", err)
		return
	}
	type item struct {
		id, tenant string
		d          Definition
	}
	var list []item
	for rows.Next() {
		var it item
		var raw json.RawMessage
		if rows.Scan(&it.id, &it.tenant, &raw) == nil && json.Unmarshal(raw, &it.d) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	for _, it := range list {
		_, vals, ok, err := forecast.HourlySeries(ctx, pool, it.tenant, it.d.DeviceID, it.d.PointID, 14*24)
		if err != nil || !ok {
			continue
		}
		m, err := forecast.Fit(vals, 24)
		if err != nil {
			continue
		}
		bt, err := forecast.RunBacktest(vals, 24, 24)
		if err != nil {
			continue
		}
		fc := m.Forecast(it.d.HorizonHours)
		step, hit := ForecastCrossing(fc, it.d.Op, it.d.Threshold, it.d.HorizonHours, bt.Useful)
		if !hit {
			continue
		}
		d := it.d
		d.Message = firstNonEmpty(d.Message, fmt.Sprintf("%s/%s is forecast to go %s %.4g in about %dh (statistical forecast, backtest error %.3g vs %.3g for repeating yesterday)",
			d.DeviceID, d.PointID, map[string]string{">": "above", "<": "below"}[d.Op], d.Threshold, step, bt.MAE, bt.NaiveMAE))
		fire(ctx, pool, n, it.tenant, it.id, d, d.DeviceID, d.PointID, fc[step-1].Value)
	}
}
