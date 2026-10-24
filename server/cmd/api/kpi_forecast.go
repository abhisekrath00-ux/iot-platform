package main

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/forecast"
	"time"
)

type kpiHour struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

// Forecast the expression of hourly input means, not a forecast of each input.
// Fail closed on any missing/undefined hour. No synthetic fill or actuation.
func projectKPI(pts []kpiHour, start time.Time, hours, horizon int) map[string]any {
	out := map[string]any{"label": "statistical", "method": "additive Holt-Winters, season 24h", "enough_data": false, "useful": false, "history_hours": hours, "complete_hours": len(pts), "note": "Expression applied to hourly input averages, not average of the expression. Complete UTC hours only; no gap filling. Nominal 95% model interval: 1.96 times training-residual sigma, widened by forecast step. Coverage not calibrated or verified on real plant data; not a guarantee. No alerts or control actions."}
	if len(pts) != hours {
		out["reason"] = "every complete hour must have all inputs and a finite expression result"
		return out
	}
	vals := make([]float64, hours)
	for i, p := range pts {
		if !p.T.Equal(start.Add(time.Duration(i) * time.Hour)) {
			out["reason"] = "hourly history is not contiguous"
			return out
		}
		vals[i] = p.V
	}
	model, err := forecast.Fit(vals, 24)
	if err != nil {
		out["reason"] = err.Error()
		return out
	}
	bt, err := forecast.RunBacktest(vals, 24, 24)
	if err != nil {
		out["reason"] = err.Error()
		return out
	}
	bt.Useful = bt.NaiveMAE > 0 && bt.MAE < 0.95*bt.NaiveMAE
	out["enough_data"] = true
	out["useful"] = bt.Useful
	out["backtest"] = map[string]any{"holdout_hours": bt.Holdout, "mae": round3(bt.MAE), "seasonal_naive_mae": round3(bt.NaiveMAE), "useful": bt.Useful}
	if !bt.Useful {
		out["reason"] = "did not beat repeating yesterday by at least 5% on the last 24 hours"
		return out
	}
	points := []map[string]any{}
	for i, p := range model.Forecast(horizon) {
		points = append(points, map[string]any{"t": start.Add(time.Duration(hours+i) * time.Hour), "value": round3(p.Value), "lower": round3(p.Lower), "upper": round3(p.Upper)})
	}
	out["forecast"] = points
	return out
}
