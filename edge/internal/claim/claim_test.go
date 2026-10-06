package claim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func okServer(hits *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		json.NewEncoder(w).Encode(map[string]string{"gateway_id": "gw1", "ingest_token": "tok"})
	}))
}

func TestRedeemAnyFallsBackWhenUnreachable(t *testing.T) {
	hits := 0
	good := okServer(&hits)
	defer good.Close()
	id, used, err := RedeemAny(context.Background(), []string{"http://127.0.0.1:1", good.URL}, "C", "S")
	if err != nil || id == nil || used != good.URL || hits != 1 {
		t.Fatalf("id=%v used=%s err=%v hits=%d", id, used, err, hits)
	}
}

func TestRedeemAnyFallsBackOn5xx(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer bad.Close()
	hits := 0
	good := okServer(&hits)
	defer good.Close()
	if _, used, err := RedeemAny(context.Background(), []string{bad.URL, good.URL}, "C", "S"); err != nil || used != good.URL {
		t.Fatalf("used=%s err=%v", used, err)
	}
}

func TestRedeemAnyStopsOnRefusal(t *testing.T) {
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer deny.Close()
	hits := 0
	good := okServer(&hits)
	defer good.Close()
	_, _, err := RedeemAny(context.Background(), []string{deny.URL, good.URL}, "C", "S")
	if !errors.Is(err, ErrRejected) || hits != 0 {
		t.Fatalf("a refused code must not be retried elsewhere: err=%v hits=%d", err, hits)
	}
}

func TestRedeemAnyAllDown(t *testing.T) {
	_, _, err := RedeemAny(context.Background(), []string{"http://127.0.0.1:1", "http://127.0.0.1:2"}, "C", "S")
	if err == nil || !strings.Contains(err.Error(), "no server address answered") || !strings.Contains(err.Error(), "127.0.0.1:2") {
		t.Fatalf("%v", err)
	}
	if _, _, err := RedeemAny(context.Background(), nil, "C", "S"); err == nil {
		t.Fatal("empty list must fail")
	}
}
