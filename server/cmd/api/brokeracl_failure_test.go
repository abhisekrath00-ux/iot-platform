package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationBrokerACLTruthfulStatus(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-aclfiles")
	dir := t.TempDir()
	path := filepath.Join(dir, "acl")
	t.Setenv("BROKER_DIRECT_PASSWD_FILE", "")
	t.Setenv("BROKER_ACL_FILE", path)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /acl", s.regenerateBrokerACLHandler)
	if w := call(mux, "itest-aclfiles", "operator", "POST", "/acl", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	t.Setenv("BROKER_ACL_FILE", "")
	if w := call(mux, "itest-aclfiles", "admin", "POST", "/acl", ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	t.Setenv("BROKER_ACL_FILE", path)
	w := call(mux, "itest-aclfiles", "admin", "POST", "/acl", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reload_required":true`) || !strings.Contains(w.Body.String(), `"status":"files_written"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.st.Close()
	w = call(mux, "itest-aclfiles", "admin", "POST", "/acl", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "files_written") {
		t.Fatal(w.Code, w.Body.String())
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("query failure changed file")
	}
}
func TestIntegrationBrokerACLWriteFailure(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-aclwrite")
	t.Setenv("BROKER_DIRECT_PASSWD_FILE", "")
	t.Setenv("BROKER_ACL_FILE", filepath.Join(t.TempDir(), "missing", "acl"))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /acl", s.regenerateBrokerACLHandler)
	w := call(mux, "itest-aclwrite", "admin", "POST", "/acl", "")
	if w.Code != 500 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE tenant_id='itest-aclwrite' AND action='brokeracl.regenerate'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("false success audit", n, e)
	}
}

func TestIntegrationBrokerACLPasswordWritePartial(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-aclpw")
	dir := t.TempDir()
	path := filepath.Join(dir, "acl")
	t.Setenv("BROKER_ACL_FILE", path)
	t.Setenv("BROKER_DIRECT_PASSWD_FILE", filepath.Join(dir, "missing", "passwd"))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /acl", s.regenerateBrokerACLHandler)
	w := call(mux, "itest-aclpw", "admin", "POST", "/acl", "")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "partially replaced") {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("expected first file before second write failure", err)
	}
	var n int
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE tenant_id='itest-aclpw' AND action='brokeracl.regenerate'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("false success audit", n, e)
	}
}
