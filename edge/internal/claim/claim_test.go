package claim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRedeemSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/enrollment/claim" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["claim_code"] != "CODE" || in["serial"] != "SER" {
			t.Errorf("bad body %v", in)
		}
		json.NewEncoder(w).Encode(map[string]string{"gateway_id": "gw1", "ingest_token": "tok"})
	}))
	defer srv.Close()
	id, err := Redeem(context.Background(), srv.URL, "CODE", "SER")
	if err != nil || id.GatewayID != "gw1" || id.IngestToken != "tok" {
		t.Fatalf("id=%v err=%v", id, err)
	}
}

func TestRedeemRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid claim", 403)
	}))
	defer srv.Close()
	if _, err := Redeem(context.Background(), srv.URL, "BAD", "SER"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIdentityRoundTripAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	id := &Identity{GatewayID: "gw1", IngestToken: "tok"}
	if err := SaveIdentity(dir, id); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dir + "/identity.json")
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms %v", fi.Mode())
	}
	got, err := LoadIdentity(dir)
	if err != nil || got.GatewayID != "gw1" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if err := SaveIdentity(dir, id); err == nil {
		t.Fatal("overwrite should fail")
	}
}
