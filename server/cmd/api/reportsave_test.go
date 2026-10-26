package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestIntegrationReportSaveChannelGuard(t *testing.T) {
	s, h := testServer(t)
	const tenant = "itest-rsave"
	seed(t, s, tenant)
	seed(t, s, "itest-rsave-other")
	ctx := t.Context()
	for _, x := range []struct{ id, tenant string }{{"rsave-own", tenant}, {"rsave-foreign", "itest-rsave-other"}} {
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES($1,$2,'email','no-send@example.invalid',false)`, x.id, x.tenant); err != nil {
			t.Fatal(err)
		}
	}
	payload := func(channel string) string {
		return fmt.Sprintf(`{"name":"Save check","definition":{"metrics":[{"device_id":"itest-rsave-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"},"channel_id":%q}`, channel)
	}
	count := func(table string) int {
		var n int
		if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, channel := range []string{"does-not-exist", "rsave-foreign"} {
		before, audits := count("reports"), count("audit_log")
		w := call(h, tenant, "admin", "POST", "/v1/reports", payload(channel))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "channel not found") {
			t.Fatalf("invalid create %s: %d %s", channel, w.Code, w.Body)
		}
		if count("reports") != before || count("audit_log") != audits {
			t.Fatal("rejected create wrote report/audit")
		}
	}
	for _, channel := range []string{"", "rsave-own"} {
		before := count("reports")
		w := call(h, tenant, "admin", "POST", "/v1/reports", payload(channel))
		if w.Code != 201 {
			t.Fatalf("create %d %s", w.Code, w.Body)
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.ID == "" {
			t.Fatal("missing ID", err)
		}
		var stored *string
		if err := s.st.Pool.QueryRow(ctx, `SELECT channel_id FROM reports WHERE id=$1 AND tenant_id=$2`, result.ID, tenant).Scan(&stored); err != nil {
			t.Fatal("201 did not persist", err)
		}
		if channel == "" && stored != nil || channel != "" && (stored == nil || *stored != channel) {
			t.Fatal("wrong stored channel", stored)
		}
		if count("reports") != before+1 {
			t.Fatal("create count")
		}
		for _, bad := range []string{"does-not-exist", "rsave-foreign"} {
			w = call(h, tenant, "admin", "PUT", "/v1/reports/"+result.ID, payload(bad))
			if w.Code != 400 {
				t.Fatalf("update %d %s", w.Code, w.Body)
			}
			var v, history int
			if err := s.st.Pool.QueryRow(ctx, `SELECT version FROM reports WHERE id=$1`, result.ID).Scan(&v); err != nil {
				t.Fatal(err)
			}
			if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_versions WHERE report_id=$1`, result.ID).Scan(&history); err != nil {
				t.Fatal(err)
			}
			if v != 1 || history != 0 {
				t.Fatal("rejected update changed history", v, history)
			}
		}
	}
	var runs int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_runs r JOIN reports p ON p.id=r.report_id WHERE p.tenant_id=$1`, tenant).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("saving ran report", runs, err)
	}
	before := count("reports")
	w := call(h, tenant, "admin", "POST", "/v1/reports", `{"name":"`+strings.Repeat("x", (1<<20)+1)+`"}`)
	if w.Code != 400 || count("reports") != before {
		t.Fatalf("oversized create %d", w.Code)
	}
	if w := call(h, tenant, "viewer", "POST", "/v1/reports", payload("")); w.Code != 403 {
		t.Fatal("viewer create", w.Code)
	}
}
