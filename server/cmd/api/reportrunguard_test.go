package main

import (
	"context"
	"encoding/json"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIntegrationReviewedRunGuard(t *testing.T) {
	s, _ := testServer(t)
	const tenant = "itest-rguard"
	seed(t, s, tenant)
	ctx := t.Context()
	def := `{"metrics":[{"device_id":"itest-rguard-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"}`
	if _, e := s.st.Pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('rguard',$1,'Synthetic',$2,'test-user')`, tenant, []byte(def)); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports/{id}/run-review", s.reviewReportRun)
	mux.HandleFunc("POST /v1/reports/{id}/run", s.runReport)
	review := func() runReview {
		w := call(mux, tenant, "admin", "GET", "/v1/reports/rguard/run-review", "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body)
		}
		var x runReview
		if e := json.Unmarshal(w.Body.Bytes(), &x); e != nil {
			t.Fatal(e)
		}
		return x
	}
	run := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/reports/rguard/run", nil)
		r.Header.Set("If-Match", token)
		r = r.WithContext(auth.AsAssistant(ctx, tenant, "test-user", "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	count := func() int {
		var n int
		if e := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_runs WHERE report_id='rguard'`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	old := review()
	if _, e := s.st.Pool.Exec(ctx, `UPDATE reports SET name='Changed',version=version+1 WHERE id='rguard'`); e != nil {
		t.Fatal(e)
	}
	if w := run(old.Token); w.Code != 409 || count() != 0 {
		t.Fatalf("stale report %d %s", w.Code, w.Body)
	}
	current := review()
	if w := run(current.Token); w.Code != 200 || count() != 1 {
		t.Fatalf("no-channel guarded run %d %s", w.Code, w.Body)
	}
	if w := run(current.Token); w.Code != 409 || count() != 1 {
		t.Fatal("replay after run accepted", w.Code)
	}
	if _, e := s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('rguard-channel',$1,'email','synthetic@example.invalid',false);`, tenant); e != nil {
		t.Fatal(e)
	}
	if _, e := s.st.Pool.Exec(ctx, `UPDATE reports SET channel_id='rguard-channel' WHERE id='rguard'`); e != nil {
		t.Fatal(e)
	}
	channelReview := review()
	for _, q := range []string{`UPDATE notification_channels SET target='other@example.invalid' WHERE id='rguard-channel'`, `UPDATE notification_channels SET target='synthetic@example.invalid' WHERE id='rguard-channel'`} {
		if _, e := s.st.Pool.Exec(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	if w := run(channelReview.Token); w.Code != 409 || count() != 1 {
		t.Fatal("channel ABA accepted", w.Code)
	}
	if _, e := s.st.Pool.Exec(ctx, `UPDATE notification_channels SET enabled=true WHERE id='rguard-channel'`); e != nil {
		t.Fatal(e)
	}
	if w := run(channelReview.Token); w.Code != 409 || count() != 1 {
		t.Fatal("enabled change accepted", w.Code)
	}
	locked := review()
	release, e := s.lockReviewedRun(ctx, tenant, "rguard", locked.Token)
	if e != nil {
		t.Fatal(e)
	}
	// Both edits must wait while the reviewed run holds its locks, without any delivery.
	for _, q := range []string{`UPDATE reports SET name='Race' WHERE id='rguard'`, `UPDATE notification_channels SET enabled=true WHERE id='rguard-channel'`} {
		c, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		_, e := s.st.Pool.Exec(c, q)
		cancel()
		if e == nil {
			release(false)
			t.Fatal("write bypassed guard lock", q)
		}
	}
	release(false)
	if _, e := s.st.Pool.Exec(ctx, `DELETE FROM notification_channels WHERE id='rguard-channel'`); e != nil {
		t.Fatal(e)
	}
	if w := run(locked.Token); w.Code != 409 || count() != 1 {
		t.Fatal("deleted channel accepted", w.Code)
	}
	if w := run("bad"); w.Code != 409 || count() != 1 {
		t.Fatal("invalid token", w.Code)
	}
	if w := call(mux, "other", "admin", "GET", "/v1/reports/rguard/run-review", ""); w.Code != 404 {
		t.Fatal("tenant leak", w.Code)
	}
	if w := call(mux, tenant, "viewer", "GET", "/v1/reports/rguard/run-review", ""); w.Code != 403 {
		t.Fatal("viewer review", w.Code)
	}
	// Headerless legacy still uses the existing no-channel path.
	if _, e := s.st.Pool.Exec(ctx, `UPDATE reports SET channel_id=NULL WHERE id='rguard'`); e != nil {
		t.Fatal(e)
	}
	if w := run(""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatal("legacy", w.Code, w.Body)
	}
}
