package main

import (
	"encoding/json"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationReportRestoreReview(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rreview")
	ctx := t.Context()
	def := []byte(`{"metrics":[{"device_id":"itest-rreview-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"}`)
	_, e := s.st.Pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('rreview','itest-rreview','Old',$1,'test-user')`, def)
	if e != nil {
		t.Fatal(e)
	}
	next := reportState{name: "Current", definition: def}
	if _, _, e = s.writeReportVersion(ctx, "itest-rreview", "rreview", "test-user", next); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports/{id}/versions", s.listReportVersions)
	mux.HandleFunc("POST /v1/reports/{id}/versions/{v}/restore", s.restoreReportVersion)
	w := call(mux, "itest-rreview", "admin", "GET", "/v1/reports/rreview/versions", "")
	var h struct {
		Current  int
		State    struct{ Name string } `json:"current_state"`
		Versions []map[string]any
	}
	if json.Unmarshal(w.Body.Bytes(), &h) != nil || h.Current != 2 || h.State.Name != "Current" || len(h.Versions) != 1 {
		t.Fatalf("history %s", w.Body)
	}
	if _, ok := h.Versions[0]["channel_id"]; !ok {
		t.Fatal("channel omitted")
	}
	restore := func(expected string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/reports/rreview/versions/1/restore", nil)
		r.Header.Set("If-Match", expected)
		r = r.WithContext(auth.AsAssistant(ctx, "itest-rreview", "test-user", "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := restore("bad"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := restore("1"); w.Code != 409 || !strings.Contains(w.Body.String(), "changed since review") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_versions WHERE report_id='rreview'`).Scan(&n)
	if n != 1 {
		t.Fatal("conflict created history", n)
	}
	if w := restore("2"); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := restore("2"); w.Code != 409 {
		t.Fatal("stale replay accepted", w.Code)
	}
	var name string
	var version int
	s.st.Pool.QueryRow(ctx, `SELECT name,version FROM reports WHERE id='rreview'`).Scan(&name, &version)
	if name != "Old" || version != 3 {
		t.Fatalf("%s %d", name, version)
	}
}
