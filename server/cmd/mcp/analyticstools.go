package main

import (
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/forecast"
)

// Statistical analytics tools. Read-only, tenant-scoped, parameters validated;
// the model never writes SQL. Every result carries label "statistical".
var analyticsTools = []map[string]any{
	{"name": "forecast_time_series", "description": "Statistical (Holt-Winters, 24h season) forecast of the hourly average for a device point, with 95% interval and a backtest against repeating yesterday. If useful is false, do not present the numbers as a prediction. Needs about 3 days of hourly data.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"device_id", "point_id"}, "properties": map[string]any{
			"device_id":     map[string]any{"type": "string"},
			"point_id":      map[string]any{"type": "string"},
			"horizon_hours": map[string]any{"type": "integer", "minimum": 1, "maximum": 72}}}},
	{"name": "related_signals", "description": "Rank the other points of the same device by lagged correlation with one point over recent days. Correlation only, never proof of cause; say 'correlated with'.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"device_id", "point_id"}, "properties": map[string]any{
			"device_id": map[string]any{"type": "string"},
			"point_id":  map[string]any{"type": "string"},
			"days":      map[string]any{"type": "integer", "minimum": 1, "maximum": 30}}}},
	{"name": "root_cause_hints", "description": "For one alert, rank points on the same device and on other devices of the same asset by lagged correlation with the alerting point. Says which moved earlier. Correlation only, never proof of cause; say 'correlated with' and 'moved earlier', not 'caused by'.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"alert_id"}, "properties": map[string]any{
			"alert_id": map[string]any{"type": "string"},
			"point_id": map[string]any{"type": "string", "description": "only if the alert's rule does not name a point"},
			"days":     map[string]any{"type": "integer", "minimum": 1, "maximum": 30}}}},
	{"name": "detect_level_shifts", "description": "Find times when a device point's hourly average settled at a new level (CUSUM on the daily-cycle-removed series). Works without a forecast. Statistical: a shift can be a real process change, a recalibration or a sensor fault, so say 'shifted' and not why. Needs about 3 days of hourly data.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"device_id", "point_id"}, "properties": map[string]any{
			"device_id": map[string]any{"type": "string"},
			"point_id":  map[string]any{"type": "string"},
			"days":      map[string]any{"type": "integer", "minimum": 3, "maximum": 30}}}},
}

func init() { tools = append(tools, analyticsTools...) }

func (s *server) analyticsTool(r *http.Request, name string, args map[string]any) (any, bool, error) {
	if name != "forecast_time_series" && name != "related_signals" && name != "root_cause_hints" && name != "detect_level_shifts" {
		return nil, false, nil
	}
	ctx, tenant := r.Context(), auth.Tenant(r)
	if name == "root_cause_hints" {
		id, _ := args["alert_id"].(string)
		if id == "" {
			return nil, true, &toolError{"alert_id required"}
		}
		pt, _ := args["point_id"].(string)
		res, err := forecast.RootCauseHints(ctx, s.st.Pool, tenant, id, pt, clampInt(args["days"], 7, 1, 30))
		if errors.Is(err, forecast.ErrAlertNotFound) || errors.Is(err, forecast.ErrNoDevice) || errors.Is(err, forecast.ErrNoPoint) {
			return nil, true, &toolError{err.Error()}
		}
		return res, true, err
	}
	dev, _ := args["device_id"].(string)
	pt, _ := args["point_id"].(string)
	if dev == "" || pt == "" {
		return nil, true, &toolError{"device_id and point_id required"}
	}
	if name == "detect_level_shifts" {
		days := clampInt(args["days"], 14, 3, 30)
		start, vals, ok, err := forecast.HourlySeries(ctx, s.st.Pool, tenant, dev, pt, days*24)
		if err != nil {
			return nil, true, err
		}
		res := map[string]any{"label": "statistical", "method": "CUSUM on the hourly average after removing the daily cycle; baseline = first 48 h, restarts after each shift",
			"caveat": "a shift is not a diagnosis: process change, recalibration and sensor fault look the same", "enough_data": ok, "shifts": []map[string]any{}}
		if !ok || len(vals) < 72 {
			res["enough_data"] = false
			return res, true, nil
		}
		res["window_from"], res["window_to"] = start, start.Add(time.Duration(len(vals))*time.Hour)
		shifts := []map[string]any{}
		des := forecast.Deseasonalize(vals, 24)
		for _, i := range forecast.CUSUM(des, 48, 0.5, 8) {
			lo := i - 24
			if lo < 0 {
				lo = 0
			}
			var before, after float64
			for _, v := range des[lo:i] {
				before += v
			}
			before /= float64(i - lo)
			hi := i + 24
			if hi > len(des) {
				hi = len(des)
			}
			for _, v := range des[i:hi] {
				after += v
			}
			after /= float64(hi - i)
			shifts = append(shifts, map[string]any{"detected_at": start.Add(time.Duration(i) * time.Hour), "mean_before": rnd(before), "mean_after": rnd(after)})
		}
		res["shifts"] = shifts
		return res, true, nil
	}
	if name == "forecast_time_series" {
		horizon := clampInt(args["horizon_hours"], 24, 1, 72)
		start, vals, ok, err := forecast.HourlySeries(ctx, s.st.Pool, tenant, dev, pt, 14*24)
		if err != nil {
			return nil, true, err
		}
		res := map[string]any{"label": "statistical", "method": "additive Holt-Winters, season 24h", "enough_data": ok}
		if !ok {
			res["reason"] = "fewer than 80% of the last 14 days of hours have data"
			return res, true, nil
		}
		m, err := forecast.Fit(vals, 24)
		if err != nil {
			res["enough_data"], res["reason"] = false, err.Error()
			return res, true, nil
		}
		bt, _ := forecast.RunBacktest(vals, 24, 24)
		next := start.Add(time.Duration(len(vals)) * time.Hour)
		var pts []map[string]any
		for i, p := range m.Forecast(horizon) {
			pts = append(pts, map[string]any{"t": next.Add(time.Duration(i) * time.Hour), "value": rnd(p.Value), "lower": rnd(p.Lower), "upper": rnd(p.Upper)})
		}
		res["forecast"], res["useful"] = pts, bt.Useful
		res["backtest"] = map[string]any{"mae": rnd(bt.MAE), "seasonal_naive_mae": rnd(bt.NaiveMAE), "holdout_hours": bt.Holdout}
		return res, true, nil
	}
	days := clampInt(args["days"], 7, 1, 30)
	_, target, ok, err := forecast.HourlySeries(ctx, s.st.Pool, tenant, dev, pt, days*24)
	if err != nil {
		return nil, true, err
	}
	res := map[string]any{"label": "statistical", "method": "lagged Pearson correlation on hourly averages, same device", "caveat": "correlated with, not caused by", "enough_data": ok, "hints": []forecast.Hint{}}
	if !ok {
		return res, true, nil
	}
	rows, err := s.st.Pool.Query(ctx, `SELECT DISTINCT point_id FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND device_id=$2 AND point_id<>$3 AND bucket > now() - make_interval(days => $4) LIMIT 40`, tenant, dev, pt, days)
	if err != nil {
		return nil, true, err
	}
	var others []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			others = append(others, p)
		}
	}
	rows.Close()
	series := map[string][]float64{}
	for _, p := range others {
		if _, v, ok, err := forecast.HourlySeries(ctx, s.st.Pool, tenant, dev, p, days*24); err == nil && ok {
			series[p] = v
		}
	}
	if h := forecast.RankRelated(target, series, 6, 0.6); h != nil {
		for i := range h {
			h[i].R = rnd(h[i].R)
		}
		res["hints"] = h
	}
	return res, true, nil
}

func rnd(f float64) float64 { return math.Round(f*1000) / 1000 }
