package claim

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCandidateRecoveryCommandRejectsCreateAndMalformedInput(t *testing.T) {
	for _, raw := range []string{`{}`, `{"Recover":false}`, `{"Create":true}`, `{"Code":"synthetic","code":"other"}`, `{"Dir":"a","Dir":"b"}`, `{} {}`, strings.Repeat("x", 131073)} {
		var out bytes.Buffer
		if e := RunCandidateRecovery(context.Background(), strings.NewReader(raw), &out); e == nil || out.Len() != 0 {
			t.Fatal("malformed recovery input accepted")
		}
	}
}

func TestCandidateRecoveryMissingStateNeverContactsServer(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(403) }))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "missing")
	run := func() {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": srv.URL, "Serial": "SER", "Code": "synthetic", "TransportCA": string(ca), "DeviceCA": string(ca), "ExpectedTenant": "tenant", "ResponseKey": pub})
		var out bytes.Buffer
		if e := RunCandidateRecovery(t.Context(), bytes.NewReader(payload), &out); e == nil || out.Len() != 0 {
			t.Fatal("missing state accepted")
		}
		if calls.Load() != 0 {
			t.Fatal("recovery contacted server without retained state")
		}
	}
	run()
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("recovery created directory")
	}
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	run()
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatal("recovery created identity state", e)
	}
	t.Log("PASS recovery-only adapter neither creates missing/partial state nor sends a request")
}

func TestCandidateNewAttemptDoesNotReplayExistingState(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(403) }))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "attempt")
	payload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": srv.URL, "Serial": "SER", "Code": "synthetic", "TransportCA": string(ca), "DeviceCA": string(ca), "ExpectedTenant": "tenant", "ResponseKey": pub})
	run := func() {
		t.Helper()
		var out bytes.Buffer
		if e := RunCandidateEnrollment(t.Context(), bytes.NewReader(payload), &out); e == nil || out.Len() != 0 {
			t.Fatal("denied enrollment accepted")
		}
	}
	run()
	if calls.Load() != 1 {
		t.Fatal("new claim count", calls.Load())
	}
	before, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil {
		t.Fatal(e)
	}
	run()
	if calls.Load() != 1 {
		t.Fatal("create replay reached server")
	}
	after, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("create replay replaced key", e)
	}
	// Partial state also blocks create locally, rather than repairing it into a
	// new enrollment or replacing the already retained identity.
	if e = os.Remove(filepath.Join(dir, "candidate-intent.json")); e != nil {
		t.Fatal(e)
	}
	run()
	if calls.Load() != 1 {
		t.Fatal("partial create replay reached server")
	}
	after, e = os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("partial state key replaced", e)
	}
}
