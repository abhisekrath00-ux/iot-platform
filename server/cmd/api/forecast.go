package main

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/forecast"
)

func intParam(r *http.Request, name string, def, lo, hi int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}

// forecastTelemetry: statistical Holt-Winters forecast of the hourly average.
// Label: statistical, not learned. The backtest says whether it beats repeating
// last season; when it does not, useful=false and the UI must not present it as a prediction.
func (s *server) forecastTelemetry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dev, pt := q.Get("device_id"), q.Get("point_id")
	if dev == "" || pt == "" {
		http.Error(w, "device_id and point_id required", 400)
		return
	}
	days, ok1 := intParam(r, "history_days", 14, 4, 60)
	horizon, ok2 := intParam(r, "horizon_hours", 24, 1, 72)
	if !ok1 || !ok2 {
		http.Error(w, "history_days must be 4-60, horizon_hours 1-72", 400)
		return
	}
	start, vals, ok, err := forecast.HourlySeries(r.Context(), s.st.Pool, auth.Tenant(r), dev, pt, days*24)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	resp := map[string]any{"label": "statistical", "method": "additive Holt-Winters, season 24h", "history_days": days, "enough_data": ok}
	if !ok {
		resp["reason"] = "fewer than 80% of the hours in the window have data"
		writeJSON(w, 200, resp)
		return
	}
	model, err := forecast.Fit(vals, 24)
	if err != nil {
		resp["enough_data"] = false
		resp["reason"] = err.Error()
		writeJSON(w, 200, resp)
		return
	}
	bt, _ := forecast.RunBacktest(vals, 24, 24)
	fc := model.Forecast(horizon)
	next := start.Add(time.Duration(len(vals)) * time.Hour)
	out := make([]map[string]any, 0, len(fc))
	for i, p := range fc {
		out = append(out, map[string]any{"t": next.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
			"value": round3(p.Value), "lower": round3(p.Lower), "upper": round3(p.Upper)})
	}
	resp["forecast"] = out
	resp["backtest"] = map[string]any{"holdout_hours": bt.Holdout, "mae": round3(bt.MAE), "seasonal_naive_mae": round3(bt.NaiveMAE), "useful": bt.Useful}
	resp["useful"] = bt.Useful
	cps := forecast.CUSUM(forecast.Deseasonalize(vals, 24), 48, 0.5, 8)
	changes := []string{}
	for _, i := range cps {
		changes = append(changes, start.Add(time.Duration(i)*time.Hour).Format(time.RFC3339))
	}
	resp["change_points"] = changes
	if v := q.Get("limit"); v != "" {
		if lim, err := strconv.ParseFloat(v, 64); err == nil {
			up := q.Get("dir") != "down"
			step := forecast.FirstCrossing(fc, lim, up)
			if step > 0 && bt.Useful {
				resp["crossing"] = map[string]any{"limit": lim, "at": next.Add(time.Duration(step-1) * time.Hour).Format(time.RFC3339)}
			}
		}
	}
	writeJSON(w, 200, resp)
}

// relatedTelemetry ranks the other points of the same device by lagged
// correlation with the given point. Correlation, never causation.
func (s *server) relatedTelemetry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dev, pt := q.Get("device_id"), q.Get("point_id")
	if dev == "" || pt == "" {
		http.Error(w, "device_id and point_id required", 400)
		return
	}
	days, ok1 := intParam(r, "days", 7, 1, 30)
	lag, ok2 := intParam(r, "max_lag_hours", 6, 0, 24)
	if !ok1 || !ok2 {
		http.Error(w, "days must be 1-30, max_lag_hours 0-24", 400)
		return
	}
	tenant := auth.Tenant(r)
	_, target, ok, err := forecast.HourlySeries(r.Context(), s.st.Pool, tenant, dev, pt, days*24)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	resp := map[string]any{"label": "statistical", "method": "lagged Pearson correlation on hourly averages, same device", "caveat": "correlated with, not caused by"}
	if !ok {
		resp["enough_data"] = false
		resp["hints"] = []forecast.Hint{}
		writeJSON(w, 200, resp)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT DISTINCT point_id FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND device_id=$2 AND point_id<>$3 AND bucket > now() - make_interval(days => $4) LIMIT 40`, tenant, dev, pt, days)
	if err != nil {
		http.Error(w, "db", 500)
		return
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
		if _, v, ok, err := forecast.HourlySeries(r.Context(), s.st.Pool, tenant, dev, p, days*24); err == nil && ok {
			series[p] = v
		}
	}
	hints := forecast.RankRelated(target, series, lag, 0.6)
	if hints == nil {
		hints = []forecast.Hint{}
	}
	for i := range hints {
		hints[i].R = math.Round(hints[i].R*1000) / 1000
	}
	resp["enough_data"], resp["hints"] = true, hints
	writeJSON(w, 200, resp)
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
