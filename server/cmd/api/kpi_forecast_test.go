package main

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestKPIProjectionGuards(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Hour).Add(-336 * time.Hour)
	pts := []kpiHour{}
	for i := 0; i < 336; i++ {
		pts = append(pts, kpiHour{start.Add(time.Duration(i) * time.Hour), 50 + 0.05*float64(i) + 10*math.Sin(2*math.Pi*float64(i%24)/24)})
	}
	p := projectKPI(pts, start, 336, 24)
	if p["enough_data"] != true || p["useful"] != true {
		t.Fatalf("trend %+v", p)
	}
	fc := p["forecast"].([]map[string]any)
	if len(fc) != 24 || !fc[0]["t"].(time.Time).Equal(start.Add(336*time.Hour)) {
		t.Fatal(fc)
	}
	if p = projectKPI(pts[1:], start, 336, 24); p["enough_data"] != false || p["forecast"] != nil {
		t.Fatal(p)
	}
	pts[0].T = pts[0].T.Add(time.Minute)
	if p = projectKPI(pts, start, 336, 24); p["enough_data"] != false {
		t.Fatal(p)
	}
	for i := range pts {
		pts[i] = kpiHour{start.Add(time.Duration(i) * time.Hour), 1}
	}
	if p = projectKPI(pts, start, 336, 24); p["useful"] != false || p["forecast"] != nil {
		t.Fatal(p)
	}
}
func TestIntegrationKPIForecast(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-kfc")
	seed(t, s, "itest-kfc2")
	ctx := t.Context()
	pool := s.st.Pool
	pool.Exec(ctx, `DELETE FROM kpis WHERE tenant_id='itest-kfc'`)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM kpis WHERE tenant_id='itest-kfc'`)
		pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-kfc'`)
	})
	pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-kfc'`)
	end := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 336; i++ {
		v := 50 + 0.05*float64(i) + 10*math.Sin(2*math.Pi*float64(i%24)/24)
		_, err := pool.Exec(ctx, `INSERT INTO telemetry_rollup_hourly(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('itest-kfc','itest-kfc-dev','temp',$1,1,$2,$2,$2) ON CONFLICT(tenant_id,device_id,point_id,bucket) DO UPDATE SET n=1,sum=$2,min=$2,max=$2`, end.Add(time.Duration(i-336)*time.Hour), v)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := pool.Exec(ctx, `INSERT INTO kpis(id,tenant_id,name,expression,unit,created_by) VALUES('itest-kfc-kpi','itest-kfc','Trend','{itest-kfc-dev.temp} * 2','C','tester')`)
	if err != nil {
		t.Fatal(err)
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/kpis/{id}/history", s.kpiHistory)
	type result struct {
		Points     []kpiHour
		Projection struct {
			EnoughData bool `json:"enough_data"`
			Useful     bool
			Forecast   []struct {
				T     time.Time
				Value float64
			}
		}
	}
	get := func(tenant, path string) result {
		t.Helper()
		w := call(api, tenant, "viewer", "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var r result
		if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	path := "/v1/kpis/itest-kfc-kpi/history?hours=336&forecast_hours=24"
	r := get("itest-kfc", path)
	if len(r.Points) != 336 || r.Points[0].V != 100 || !r.Projection.Useful || len(r.Projection.Forecast) != 24 || !r.Projection.Forecast[0].T.Equal(end) {
		t.Fatalf("%+v", r.Projection)
	}
	for _, q := range []string{"hours=24&forecast_hours=24", "hours=336&forecast_hours=0", "hours=336&forecast_hours=73", "hours=336&forecast_hours=x"} {
		if call(api, "itest-kfc", "viewer", "GET", "/v1/kpis/itest-kfc-kpi/history?"+q, "").Code != 400 {
			t.Fatal(q)
		}
	}
	if call(api, "itest-kfc2", "viewer", "GET", path, "").Code != 404 {
		t.Fatal("cross tenant")
	}
	if scopedAllows("GET", "/v1/kpis/itest-kfc-kpi/history") {
		t.Fatal("scoped KPI exposure")
	}
	// A second missing input must withhold the entire projection, even when the first is dense.
	pool.Exec(ctx, `UPDATE kpis SET expression='{itest-kfc-dev.temp} + {itest-kfc-dev.absent}' WHERE id='itest-kfc-kpi'`)
	if r = get("itest-kfc", path); r.Projection.EnoughData || len(r.Points) > 0 {
		t.Fatal("missing second input")
	}
	pool.Exec(ctx, `UPDATE kpis SET expression='{itest-kfc-dev.temp} * 2' WHERE id='itest-kfc-kpi'`)
	pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-kfc' AND bucket=$1`, end.Add(-100*time.Hour))
	if r = get("itest-kfc", path); r.Projection.EnoughData || len(r.Projection.Forecast) > 0 {
		t.Fatal("gap forecast")
	}
	pool.Exec(ctx, `UPDATE kpis SET expression='1 / ({itest-kfc-dev.temp} - {itest-kfc-dev.temp})' WHERE id='itest-kfc-kpi'`)
	if r = get("itest-kfc", path); r.Projection.EnoughData || len(r.Points) > 0 {
		t.Fatal("undefined math")
	}
}
