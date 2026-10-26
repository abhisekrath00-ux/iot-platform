package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationReportRunHistory(t *testing.T) {
	s, _ := testServer(t)
	const tenant = "itest-runs"
	seed(t, s, tenant)
	seed(t, s, "itest-runs-other")
	ctx := t.Context()
	for _, x := range []struct{ id, tenant string }{{"runs-own", tenant}, {"runs-empty", tenant}, {"runs-foreign", "itest-runs-other"}} {
		if _, e := s.st.Pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES($1,$2,'Synthetic','{}','test-user')`, x.id, x.tenant); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 55; i++ {
		status := "ok"
		if i == 54 {
			status = "running"
		}
		if i == 53 {
			status = "failed"
		}
		if _, e := s.st.Pool.Exec(ctx, `INSERT INTO report_runs(id,report_id,started_at,finished_at,status,error,row_count) VALUES($1,'runs-own',now()+$2*interval '1 second',CASE WHEN $3='running' THEN NULL ELSE now() END,$3,'private-provider-token@example.invalid',CASE WHEN $3='ok' THEN 12 ELSE NULL END)`, fmt.Sprintf("run-%02d", i), i, status); e != nil {
			t.Fatal(e)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports/{id}/runs", s.listReportRuns)
	w := call(mux, tenant, "viewer", "GET", "/v1/reports/runs-own/runs", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	var out struct {
		Runs []struct {
			ID, Status string
			Rows       *int `json:"row_count"`
		}
		Limit int
	}
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Runs) != 50 || out.Limit != 50 || out.Runs[0].ID != "run-54" || out.Runs[1].Status != "failed" || out.Runs[2].Rows == nil || *out.Runs[2].Rows != 12 {
		t.Fatalf("bad history %+v", out)
	}
	if strings.Contains(w.Body.String(), "private-provider") || strings.Contains(w.Body.String(), `"error"`) {
		t.Fatal("raw error leaked")
	}
	for _, id := range []string{"runs-foreign", "missing"} {
		if w := call(mux, tenant, "viewer", "GET", "/v1/reports/"+id+"/runs", ""); w.Code != 404 {
			t.Fatal(id, w.Code)
		}
	}
	w = call(mux, tenant, "viewer", "GET", "/v1/reports/runs-empty/runs", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"runs":[]`) {
		t.Fatal(w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/v1/reports/runs-own/runs", nil)
	r = r.WithContext(context.WithValue(r.Context(), scopeKey{}, "customer"))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("scoped", w.Code)
	}
	if scopedAllows("GET", "/v1/reports/runs-own/runs") {
		t.Fatal("scope allowlist widened")
	}
}
