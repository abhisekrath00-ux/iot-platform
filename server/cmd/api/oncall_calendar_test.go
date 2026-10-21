package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

func calReq(t *testing.T, method, path, body, role string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	c := context.WithValue(r.Context(), auth.CtxTenant, "itest-cal")
	c = context.WithValue(c, auth.CtxUser, "itest-cal-admin")
	c = context.WithValue(c, auth.CtxRole, role)
	return r.WithContext(c)
}

// Link -> sync derives the on-call channel from the calendar event spanning
// now; unlink clears; error and RBAC paths. Google simulator throughout.
// Requires TEST_DATABASE_URL (CI); skips locally.
func TestIntegrationOnCallCalendarSync(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cal")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: key}
	ctx := context.Background()

	now := time.Now().UTC()
	spanStart := now.Add(-time.Hour).Format(time.RFC3339)
	spanEnd := now.Add(6 * time.Hour).Format(time.RFC3339)
	sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" { // token endpoint
			json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.sim", "expires_in": 3600, "token_type": "Bearer"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"summary": "Night shift: noc@hex.example", "start": map[string]string{"dateTime": spanStart}, "end": map[string]string{"dateTime": spanEnd}},
			{"summary": "Future: ops@hex.example", "start": map[string]string{"dateTime": now.Add(8 * time.Hour).Format(time.RFC3339)}, "end": map[string]string{"dateTime": now.Add(16 * time.Hour).Format(time.RFC3339)}},
		}})
	}))
	t.Cleanup(sim.Close)
	p, _ := googlews.NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	p.TokenURL = sim.URL
	p.CalendarURL = sim.URL
	s.google = p
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM oncall_schedules WHERE tenant_id='itest-cal'`,
			`DELETE FROM notification_channels WHERE tenant_id='itest-cal'`,
			`DELETE FROM secrets WHERE tenant_id='itest-cal'`,
			`DELETE FROM audit_log WHERE tenant_id='itest-cal'`,
		} {
			s.st.Pool.Exec(ctx, q)
		}
	})

	// two channels; the static rotation would pick ops (index 0)
	s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('ch-ops','itest-cal','email','ops@hex.example',true),('ch-noc','itest-cal','email','noc@hex.example',true)`)
	anchor := now.Add(-time.Hour)
	s.st.Pool.Exec(ctx, `INSERT INTO oncall_schedules(id,tenant_id,name,anchor,shift_hours,channel_ids,created_by) VALUES('sc-1','itest-cal','rot',$1,24,'{ch-ops,ch-noc}','test')`, anchor)

	// viewer refused
	rec := httptest.NewRecorder()
	s.putOnCallCalendar(rec, calReq(t, "PUT", "/v1/oncall/sc-1/calendar", `{"calendar_id":"cal-1"}`, "viewer"))
	if rec.Code != 403 {
		t.Fatalf("viewer: %d", rec.Code)
	}
	// unknown schedule 404 (set the path value like the router would)
	rec = httptest.NewRecorder()
	r404 := calReq(t, "PUT", "/v1/oncall/nope/calendar", `{"calendar_id":"cal-1"}`, "admin")
	r404.SetPathValue("id", "nope")
	s.putOnCallCalendar(rec, r404)
	if rec.Code != 404 {
		t.Fatalf("unknown schedule: %d", rec.Code)
	}
	// not connected -> 409
	rec = httptest.NewRecorder()
	r409 := calReq(t, "PUT", "/v1/oncall/sc-1/calendar", `{"calendar_id":"cal-1"}`, "admin")
	r409.SetPathValue("id", "sc-1")
	s.putOnCallCalendar(rec, r409)
	if rec.Code != 409 {
		t.Fatalf("not connected: %d %s", rec.Code, rec.Body.String())
	}
	// connect the tenant (store a refresh token directly; consent flow is covered elsewhere)
	if err := googlews.SaveRefreshToken(ctx, s.secrets, "itest-cal", "test", &googlews.Tokens{RefreshToken: "1//sim-rt"}); err != nil {
		t.Fatal(err)
	}
	// link: validates against the sim, syncs immediately
	rec = httptest.NewRecorder()
	rOK := calReq(t, "PUT", "/v1/oncall/sc-1/calendar", `{"calendar_id":"cal-1"}`, "admin")
	rOK.SetPathValue("id", "sc-1")
	s.putOnCallCalendar(rec, rOK)
	if rec.Code != 200 {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	var linkOut map[string]any
	json.NewDecoder(rec.Body).Decode(&linkOut)
	if linkOut["cal_channel_id"] != "ch-noc" {
		t.Fatalf("override should name the channel in the current event: %v", linkOut)
	}
	// listOnCall honors the override even though the rotation says ch-ops
	rec = httptest.NewRecorder()
	s.listOnCall(rec, calReq(t, "GET", "/v1/oncall", "", "admin"))
	var lst []map[string]any
	json.NewDecoder(rec.Body).Decode(&lst)
	if len(lst) != 1 || lst[0]["on_call_channel"] != "ch-noc" || lst[0]["google_calendar_id"] != "cal-1" {
		t.Fatalf("listOnCall with override: %+v", lst)
	}
	// unlink clears everything
	rec = httptest.NewRecorder()
	rUn := calReq(t, "PUT", "/v1/oncall/sc-1/calendar", `{"calendar_id":""}`, "admin")
	rUn.SetPathValue("id", "sc-1")
	s.putOnCallCalendar(rec, rUn)
	if rec.Code != 200 {
		t.Fatalf("unlink: %d", rec.Code)
	}
	var calID string
	var calCh *string
	s.st.Pool.QueryRow(ctx, `SELECT google_calendar_id, cal_channel_id FROM oncall_schedules WHERE id='sc-1'`).Scan(&calID, &calCh)
	if calID != "" || calCh != nil {
		t.Fatalf("unlink must clear: %q %v", calID, calCh)
	}
}

// A current event that names no channel target falls back to the rotation
// with an explanatory sync error - escalation never breaks on calendar data.
func TestIntegrationOnCallCalendarNoMatch(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cal")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 9)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: key}
	ctx := context.Background()
	now := time.Now().UTC()
	sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.sim", "expires_in": 3600, "token_type": "Bearer"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"summary": "Night shift: stranger@other.example", "start": map[string]string{"dateTime": now.Add(-time.Hour).Format(time.RFC3339)}, "end": map[string]string{"dateTime": now.Add(6 * time.Hour).Format(time.RFC3339)}},
		}})
	}))
	t.Cleanup(sim.Close)
	p, _ := googlews.NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	p.TokenURL = sim.URL
	p.CalendarURL = sim.URL
	s.google = p
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM oncall_schedules WHERE tenant_id='itest-cal'`,
			`DELETE FROM notification_channels WHERE tenant_id='itest-cal'`,
			`DELETE FROM secrets WHERE tenant_id='itest-cal'`,
			`DELETE FROM audit_log WHERE tenant_id='itest-cal'`,
		} {
			s.st.Pool.Exec(ctx, q)
		}
	})
	s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('ch-ops','itest-cal','email','ops@hex.example',true)`)
	s.st.Pool.Exec(ctx, `INSERT INTO oncall_schedules(id,tenant_id,name,anchor,shift_hours,channel_ids,created_by) VALUES('sc-2','itest-cal','rot2',$1,24,'{ch-ops}','test')`, now.Add(-time.Hour))
	if err := googlews.SaveRefreshToken(ctx, s.secrets, "itest-cal", "test", &googlews.Tokens{RefreshToken: "1//sim-rt"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r := calReq(t, "PUT", "/v1/oncall/sc-2/calendar", `{"calendar_id":"cal-1"}`, "admin")
	r.SetPathValue("id", "sc-2")
	s.putOnCallCalendar(rec, r)
	if rec.Code != 200 {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["cal_channel_id"] != "" {
		t.Fatalf("no match must leave the override empty: %v", out)
	}
	if !strings.Contains(fmt.Sprint(out["cal_sync_error"]), "names no channel target") {
		t.Fatalf("sync error should explain the fallback: %v", out)
	}
}
