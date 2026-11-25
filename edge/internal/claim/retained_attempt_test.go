package claim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRetainedAttemptBeforePostAndUncertainRefusesReplay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attempt")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		for _, name := range []string{"identity.key", "intent.json", "request-started"} {
			if _, e := os.Stat(filepath.Join(dir, name)); e != nil {
				t.Errorf("not retained before POST: %s", name)
			}
		}
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		intent, _ := os.ReadFile(filepath.Join(dir, "intent.json"))
		if strings.Contains(string(intent), "synthetic-secret") {
			t.Error("code stored")
		}
		if !strings.Contains(string(intent), "csr_pem") || b["csr_pem"] == "" {
			t.Error("CSR missing")
		}
		w.WriteHeader(502)
	}))
	defer srv.Close()
	_, err := RetainedClaimOnce(context.Background(), srv.Client(), dir, srv.URL, "synthetic-secret", "SER")
	if err == nil {
		t.Fatal("expected uncertainty")
	}
	key, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	_, err = RetainedClaimOnce(context.Background(), srv.Client(), dir, srv.URL, "synthetic-secret", "SER")
	if err == nil || hits != 1 {
		t.Fatal("replayed")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	if string(after) != string(key) {
		t.Fatal("key changed")
	}
}
func TestRetainedAttemptRedirectNeverLeaksClaim(t *testing.T) {
	var target int
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { target++ }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, 307) }))
	defer src.Close()
	_, e := RetainedClaimOnce(context.Background(), src.Client(), filepath.Join(t.TempDir(), "a"), src.URL, "C", "S")
	if e == nil || target != 0 {
		t.Fatal("redirect followed")
	}
}
func TestRetainedAttemptConcurrentAndPartialPreparationRefusal(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"gateway_id": "gw", "ingest_token": "synthetic-token"})
	}))
	defer srv.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "a")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = RetainedClaimOnce(context.Background(), srv.Client(), dir, srv.URL, "C", "S")
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("POSTs %d", n.Load())
	}
	p := filepath.Join(root, "partial")
	_ = os.Mkdir(p, 0700)
	if _, e := RetainedClaimOnce(context.Background(), srv.Client(), p, srv.URL, "C", "S"); e == nil || n.Load() != 1 {
		t.Fatal("partial replay")
	}
	raw, e := os.ReadFile(filepath.Join(dir, "response.json"))
	if e != nil || !strings.Contains(string(raw), "gateway_id") {
		t.Fatal("response not retained")
	}
}
func TestRetainedAttemptBadURLHasNoPreparation(t *testing.T) {
	for _, server := range []string{"http://outside.invalid", "https://example.invalid?code=x", "https://u:p@example.invalid", "https://example.invalid/path"} {
		d := filepath.Join(t.TempDir(), "a")
		if _, e := RetainedClaimOnce(context.Background(), http.DefaultClient, d, server, "C", "S"); e == nil {
			t.Fatal("bad URL accepted")
		}
		if _, e := os.Stat(d); !os.IsNotExist(e) {
			t.Fatal("prepared before URL gate")
		}
	}
}
