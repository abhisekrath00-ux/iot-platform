package main

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationReportEditVersionGuard(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	tenant := "itest-editguard"
	seed(t, s, tenant)
	def := []byte(`{"metrics":[{"device_id":"itest-editguard-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"}`)
	_, err := s.st.Pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('editguard',$1,'Original',$2,'test-user')`, tenant, def)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/reports/{id}", s.updateReport)
	save := func(version, name, role, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/v1/reports/"+id, strings.NewReader(`{"name":"`+name+`","definition":`+string(def)+`}`))
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		r = r.WithContext(auth.AsAssistant(ctx, tenant, "test-user", role))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, v := range []string{"bad", "0", "-1", "1,2", "\"1\""} {
		if w := save(v, "Invalid", "admin", "editguard"); w.Code != 400 {
			t.Fatalf("%q=%d %s", v, w.Code, w.Body)
		}
	}
	if w := save("1", "First editor", "admin", "editguard"); w.Code != 200 {
		t.Fatalf("first %d %s", w.Code, w.Body)
	}
	if w := save("1", "Stale second editor", "admin", "editguard"); w.Code != 409 {
		t.Fatalf("stale %d %s", w.Code, w.Body)
	}
	var name string
	var ver, n, audits int
	if err = s.st.Pool.QueryRow(ctx, `SELECT name,version FROM reports WHERE id='editguard'`).Scan(&name, &ver); err != nil {
		t.Fatal(err)
	}
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_versions WHERE report_id='editguard'`).Scan(&n)
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND action='report.update'`, tenant).Scan(&audits)
	if name != "First editor" || ver != 2 || n != 1 || audits != 1 {
		t.Fatalf("conflict mutated state: %s %d history%d audits%d", name, ver, n, audits)
	}
	if w := save("2", "Viewer", "viewer", "editguard"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := save("2", "Other report", "admin", "missing"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := save("2", "Reopened editor", "operator", "editguard"); w.Code != 200 {
		t.Fatalf("reopen %d %s", w.Code, w.Body)
	}
	if w := save("2", "Replay", "admin", "editguard"); w.Code != 409 {
		t.Fatal("replay", w.Code)
	}
	if w := save("", "Legacy caller", "admin", "editguard"); w.Code != 200 {
		t.Fatal("headerless compatibility", w.Code, w.Body)
	}
}
