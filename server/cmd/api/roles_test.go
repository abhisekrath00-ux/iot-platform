package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

func TestDeniedByRole(t *testing.T) {
	d := []string{"write:flows", "read:reports"}
	for _, c := range []struct {
		m, p string
		want bool
	}{
		{"POST", "/v1/flows", true}, {"PUT", "/v1/flows/x/publish", true}, {"GET", "/v1/flows", false},
		{"GET", "/v1/reports", true}, {"GET", "/v1/reports/abc/download", true}, {"GET", "/v1/devices", false},
		{"GET", "/v1/flows-other", false}, {"POST", "/v1/flow-fragments", true}, {"POST", "/v1/flowsx", false},
	} {
		if got := deniedByRole(d, c.m, c.p); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.m, c.p, got, c.want)
		}
	}
}

func TestIntegrationCustomRoles(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cr1")
	seed(t, s, "itest-cr2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `UPDATE users SET custom_role_id=NULL WHERE tenant_id IN ('itest-cr1','itest-cr2')`)
		pool.Exec(ctx, `DELETE FROM custom_roles WHERE tenant_id IN ('itest-cr1','itest-cr2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-cr1','itest-cr2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('cr-admin','cr-op','cr-admin2')`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('cr-admin','itest-cr1','cr-admin@cr-test.example','A','admin'),('cr-op','itest-cr1','cr-op@cr-test.example','O','operator'),('cr-admin2','itest-cr2','cr-admin2@cr-test.example','A2','admin')`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/roles", s.listRoles)
	mux.HandleFunc("POST /v1/roles", s.createRole)
	mux.HandleFunc("PUT /v1/roles/{id}", s.updateRole)
	mux.HandleFunc("DELETE /v1/roles/{id}", s.deleteRole)
	mux.HandleFunc("PUT /v1/users/{id}", s.updateUser)
	mux.HandleFunc("GET /v1/me", s.me)
	ok := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok " + auth.Role(r))) }
	mux.HandleFunc("POST /v1/flows", ok)
	mux.HandleFunc("GET /v1/flows", ok)
	mux.HandleFunc("GET /v1/reports", ok)
	mux.HandleFunc("GET /v1/devices", ok)
	h := s.activeUser(mux)
	adm := func(method, path, body string) *httptest.ResponseRecorder {
		return callAs(h, "itest-cr1", "cr-admin", "admin", method, path, body)
	}
	op := func(method, path string) *httptest.ResponseRecorder {
		return callAs(h, "itest-cr1", "cr-op", "operator", method, path, "")
	}
	if callAs(h, "itest-cr1", "cr-op", "operator", "POST", "/v1/roles", `{"name":"x","base_role":"viewer"}`).Code != 403 {
		t.Fatal("operator created a role")
	}
	for _, bad := range []string{
		`{"name":"x","base_role":"admin"}`, `{"name":"x","base_role":"viewer","denied":["nope"]}`,
		`{"name":"x","base_role":"viewer","denied":["write:nothing"]}`, `{"name":"","base_role":"viewer"}`, `{"name":"x","base_role":"root"}`,
	} {
		if c := adm("POST", "/v1/roles", bad).Code; c != 400 {
			t.Fatalf("%s = %d", bad, c)
		}
	}
	w := adm("POST", "/v1/roles", `{"name":"Analyst","base_role":"operator","denied":["write:flows","read:reports"]}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var rl struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &rl)
	if adm("POST", "/v1/roles", `{"name":"Analyst","base_role":"viewer"}`).Code != 409 {
		t.Fatal("duplicate name")
	}
	// before assignment the operator can do everything the base role allows
	if op("POST", "/v1/flows").Code != 200 || op("GET", "/v1/reports").Code != 200 {
		t.Fatal("baseline")
	}
	if c := adm("PUT", "/v1/users/cr-op", `{"custom_role_id":"`+rl.ID+`"}`).Code; c != 204 {
		t.Fatalf("assign = %d", c)
	}
	if op("POST", "/v1/flows").Code != 403 || op("GET", "/v1/reports").Code != 403 {
		t.Fatal("denied groups still reachable")
	}
	if op("GET", "/v1/flows").Code != 200 || op("GET", "/v1/devices").Code != 200 {
		t.Fatal("allowed paths were blocked")
	}
	// the assistant runs as the user and is limited by the same role
	ar := httptest.NewRequest("POST", "/v1/flows", nil).WithContext(auth.AsAssistant(ctx, "itest-cr1", "cr-op", "operator"))
	aw := httptest.NewRecorder()
	h.ServeHTTP(aw, ar)
	if aw.Code != 403 {
		t.Fatalf("assistant bypassed the custom role: %d", aw.Code)
	}
	if me := op("GET", "/v1/me"); !strings.Contains(me.Body.String(), "write:flows") {
		t.Fatalf("me must list the denied groups: %s", me.Body)
	}
	// another tenant cannot see, change or assign it
	if callAs(h, "itest-cr2", "cr-admin2", "admin", "PUT", "/v1/roles/"+rl.ID, `{"name":"Analyst","base_role":"viewer"}`).Code != 404 {
		t.Fatal("cross-tenant update")
	}
	if callAs(h, "itest-cr2", "cr-admin2", "admin", "DELETE", "/v1/roles/"+rl.ID, "").Code != 404 {
		t.Fatal("cross-tenant delete")
	}
	if l := callAs(h, "itest-cr2", "cr-admin2", "admin", "GET", "/v1/roles", ""); strings.Contains(l.Body.String(), rl.ID) {
		t.Fatal("cross-tenant list")
	}
	if callAs(h, "itest-cr2", "cr-admin2", "admin", "PUT", "/v1/users/cr-admin2", `{"custom_role_id":"`+rl.ID+`"}`).Code != 404 {
		t.Fatal("assigned another tenant's role")
	}
	// the base role follows an edit; a role in use cannot be deleted
	if adm("PUT", "/v1/roles/"+rl.ID, `{"name":"Analyst","base_role":"viewer","denied":[]}`).Code != 204 {
		t.Fatal("update")
	}
	var base string
	pool.QueryRow(ctx, `SELECT role FROM users WHERE id='cr-op'`).Scan(&base)
	if base != "viewer" {
		t.Fatalf("member base role = %s", base)
	}
	if adm("DELETE", "/v1/roles/"+rl.ID, "").Code != 409 {
		t.Fatal("deleted a role in use")
	}
	// choosing a built-in role clears the custom one
	if adm("PUT", "/v1/users/cr-op", `{"role":"operator"}`).Code != 204 {
		t.Fatal("builtin role")
	}
	var cr *string
	pool.QueryRow(ctx, `SELECT custom_role_id FROM users WHERE id='cr-op'`).Scan(&cr)
	if cr != nil {
		t.Fatal("custom role not cleared")
	}
	if adm("DELETE", "/v1/roles/"+rl.ID, "").Code != 204 {
		t.Fatal("delete")
	}
}
