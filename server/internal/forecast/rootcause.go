package forecast

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlertNotFound = errors.New("alert not found")
	ErrNoDevice      = errors.New("this alert has no device, so there is nothing to compare")
	ErrNoPoint       = errors.New("point_id is needed: this alert's rule does not name a point")
)

// RootHint is one point whose hourly average moves with the alerting point.
type RootHint struct {
	DeviceID string  `json:"device_id"`
	PointID  string  `json:"point_id"`
	R        float64 `json:"r"`
	// LagHours > 0: this point moved first, by that many hours. < 0: it moved after.
	LagHours int    `json:"lag_hours"`
	Reading  string `json:"reading"`
}

// RootCause is the result of RootCauseHints. It is correlation, never proof of cause.
type RootCause struct {
	Label      string     `json:"label"`
	Method     string     `json:"method"`
	Caveat     string     `json:"caveat"`
	AlertID    string     `json:"alert_id"`
	DeviceID   string     `json:"device_id"`
	PointID    string     `json:"point_id"`
	WindowFrom time.Time  `json:"window_from"`
	WindowTo   time.Time  `json:"window_to"`
	EnoughData bool       `json:"enough_data"`
	Reason     string     `json:"reason,omitempty"`
	Examined   int        `json:"examined"`
	Hints      []RootHint `json:"hints"`
}

// Reading words one hint without claiming cause.
func Reading(dev, pt string, r float64, lag int) string {
	how := "rises and falls together with it"
	if r < 0 {
		how = "moves the opposite way"
	}
	when := "at the same time"
	switch {
	case lag > 0:
		when = fmt.Sprintf("about %d h earlier", lag)
	case lag < 0:
		when = fmt.Sprintf("about %d h later", -lag)
	}
	return fmt.Sprintf("%s / %s %s (r = %.2f), moving %s than the alerting point", dev, pt, how, r, when)
}

// RootCauseHints ranks the other points of the alert's device, and of the other devices on the
// same asset, by lagged correlation of hourly averages with the alerting point over `days`
// days ending at the hour the alert was raised. pointOverride names the point when the alert's
// rule does not. Statistical only: no model, nothing is written, and the wording is
// "correlated with, moved earlier", never "caused by".
func RootCauseHints(ctx context.Context, pool *pgxpool.Pool, tenant, alertID, pointOverride string, days int) (*RootCause, error) {
	var dev *string
	var created time.Time
	var rulePoint *string
	err := pool.QueryRow(ctx,
		`SELECT a.device_id, a.created_at, r.definition->>'point_id' FROM alerts a LEFT JOIN rules r ON r.id=a.rule_id
		 WHERE a.id=$1 AND a.tenant_id=$2`, alertID, tenant).Scan(&dev, &created, &rulePoint)
	if err == pgx.ErrNoRows {
		return nil, ErrAlertNotFound
	}
	if err != nil {
		return nil, err
	}
	if dev == nil || *dev == "" {
		return nil, ErrNoDevice
	}
	pt := pointOverride
	if pt == "" && rulePoint != nil {
		pt = *rulePoint
	}
	if pt == "" {
		return nil, ErrNoPoint
	}
	if days < 1 {
		days = 1
	}
	hours := days * 24
	at := created.UTC().Truncate(time.Hour).Add(time.Hour)
	if now := time.Now().UTC(); at.After(now) {
		at = now
	}
	end := at.Truncate(time.Hour)
	res := &RootCause{Label: "statistical", Method: "lagged Pearson correlation on hourly averages, same device and other devices on the same asset",
		Caveat: "correlated with, not caused by", AlertID: alertID, DeviceID: *dev, PointID: pt,
		WindowFrom: end.Add(-time.Duration(hours) * time.Hour), WindowTo: end, Hints: []RootHint{}}
	_, target, ok, err := HourlySeriesEnding(ctx, pool, tenant, *dev, pt, hours, at)
	if err != nil {
		return nil, err
	}
	if !ok {
		res.Reason = "fewer than 80% of the hours in the window have data for the alerting point"
		return res, nil
	}
	rows, err := pool.Query(ctx,
		`WITH devs AS (SELECT $2::text AS id UNION SELECT d.id FROM devices d WHERE d.tenant_id=$1
		                AND d.asset_id IS NOT NULL AND d.asset_id=(SELECT asset_id FROM devices WHERE id=$2 AND tenant_id=$1)),
		 c AS (SELECT device_id, point_id FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND bucket >= $3 AND bucket < $4 AND device_id IN (SELECT id FROM devs)
		       UNION SELECT device_id, point_id FROM telemetry WHERE tenant_id=$1 AND observed_at >= $3 AND observed_at < $4 AND device_id IN (SELECT id FROM devs))
		 SELECT DISTINCT device_id, point_id FROM c WHERE NOT (device_id=$2 AND point_id=$5) ORDER BY 1,2 LIMIT 60`, tenant, *dev, res.WindowFrom, end, pt)
	if err != nil {
		return nil, err
	}
	type cand struct{ dev, pt string }
	var cands []cand
	for rows.Next() {
		var c cand
		if rows.Scan(&c.dev, &c.pt) == nil {
			cands = append(cands, c)
		}
	}
	rows.Close()
	series := map[string][]float64{}
	for _, c := range cands {
		if _, v, ok, err := HourlySeriesEnding(ctx, pool, tenant, c.dev, c.pt, hours, at); err == nil && ok {
			series[c.dev+"\x00"+c.pt] = v
		}
	}
	res.Examined = len(series)
	res.EnoughData = true
	for _, h := range RankRelated(target, series, 6, 0.6) {
		d, p, _ := strings.Cut(h.Name, "\x00")
		r := math.Round(h.R*1000) / 1000
		res.Hints = append(res.Hints, RootHint{DeviceID: d, PointID: p, R: r, LagHours: h.Lag, Reading: Reading(d, p, r, h.Lag)})
	}
	if len(res.Hints) > 10 {
		res.Hints = res.Hints[:10]
	}
	return res, nil
}
