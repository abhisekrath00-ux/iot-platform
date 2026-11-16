package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func trust(t *testing.T) (ReceiptTrust, ed25519.PrivateKey) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return ReceiptTrust{Enabled: true, Audience: "isolated-synthetic-deployment", TenantID: "tenant", GatewayID: "gateway", PublicKey: pub}, key
}
func token(t *testing.T, r Receipt, c ReceiptTrust, key ed25519.PrivateKey) []byte {
	t.Helper()
	sum := sha256.Sum256(c.PublicKey)
	p := signedPayload{Version: 1, KeyID: hex.EncodeToString(sum[:]), Audience: c.Audience, Receipt: r}
	b, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	return signedBytes(t, b, key)
}
func signedBytes(t *testing.T, b []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	sig := ed25519.Sign(key, append([]byte(receiptDomain), b...))
	w, e := json.Marshal(signedWire{Payload: b, Signature: sig})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func TestServerSignedFixture(t *testing.T) {
	p := os.Getenv("TEST_SERVER_SIGNED_RECEIPT")
	if p == "" {
		t.Skip("server cross-module bridge only")
	}
	var f struct {
		PublicKey  []byte
		Audience   string
		Reading    Reading
		Wire       []byte
		ReplayWire []byte
	}
	b, e := os.ReadFile(p)
	if e != nil || json.Unmarshal(b, &f) != nil {
		t.Fatal("fixture unreadable", e)
	}
	s := openStore(t, config(t, 2))
	it := mustPut(t, s, f.Reading)
	c := ReceiptTrust{Enabled: true, Audience: f.Audience, TenantID: f.Reading.TenantID, GatewayID: f.Reading.GatewayID, PublicKey: f.PublicKey}
	ok, e := s.ReconcileSigned(t.Context(), it.Sequence, f.Wire, c)
	if !ok || e != nil {
		t.Fatal(ok, e)
	}
	rows, e := s.Next(t.Context(), 2)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	t.Log("SERVER_SIGNED_ATOMIC_RECONCILIATION_OK")
	// Feed actual server-produced initial/replay evidence through the bounded
	// publisher contract. The first synthetic exchange loses the response AFTER
	// that real server admission. A later explicit call uses the actual terminal
	// replay signature. This is NOT network delivery or runtime transport proof.
	staged := openStore(t, config(t, 1))
	retained := mustPut(t, staged, f.Reading)
	calls := 0
	pub := publisher(t, staged, c, func(_ context.Context, sent Item) ([]byte, error) {
		calls++
		if sent.Sequence != retained.Sequence || !bytes.Equal(sent.Payload, retained.Payload) {
			t.Fatal("identity substituted")
		}
		if calls == 1 {
			if _, e := verifyReceipt(f.Wire, c); e != nil {
				t.Fatal(e)
			}
			return nil, errors.New("synthetic lost server admission response")
		}
		return f.ReplayWire, nil
	}, 1)
	if r, e := pub.RunOnce(t.Context()); e == nil || r.Attempted != 1 || r.Reconciled != 0 {
		t.Fatal(r, e)
	}
	if rows, e := staged.Next(t.Context(), 1); e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if r, e := pub.RunOnce(t.Context()); e != nil || r.Attempted != 1 || r.Reconciled != 1 {
		t.Fatal(r, e)
	}
	if rows, e := staged.Next(t.Context(), 1); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	t.Log("SERVER_SIGNED_EXPLICIT_REPLAY_PUBLISHER_OK")
	// Actual network leg uses disposable loopback mTLS and ACTUAL server-produced
	// PG signatures. The fixture handler only forwards precomputed evidence: it is
	// not a production authenticated admission endpoint or runtime enablement.
	tlsFixture := mtlsFixture(t)
	networkStore := openStore(t, config(t, 1))
	networkItem := mustPut(t, networkStore, f.Reading)
	var posts atomic.Int32
	server := tlsServer(t, tlsFixture, func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil || !bytes.Equal(body, networkItem.Payload) || r.Header.Get("X-HexThings-Telemetry-Topic") != networkItem.Topic {
			t.Error("network identity changed", e)
			w.WriteHeader(400)
			return
		}
		n := posts.Add(1)
		if n == 1 {
			conn, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(f.ReplayWire)
	})
	networkExchange := exchange(t, tlsFixture, server.URL+"/isolated-admission")
	networkPublisher, e := NewPublisher(PublisherConfig{Enabled: true, Store: networkStore, Trust: c, Exchange: networkExchange, BatchLimit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if r, e := networkPublisher.RunOnce(t.Context()); e == nil || r.Attempted != 1 || r.Reconciled != 0 || posts.Load() != 1 {
		t.Fatal(r, e, posts.Load())
	}
	if rows, e := networkStore.Next(t.Context(), 1); e != nil || len(rows) != 1 || rows[0].Sequence != networkItem.Sequence {
		t.Fatal(rows, e)
	}
	if r, e := networkPublisher.RunOnce(t.Context()); e != nil || r.Attempted != 1 || r.Reconciled != 1 || posts.Load() != 2 {
		t.Fatal(r, e, posts.Load())
	}
	if rows, e := networkStore.Next(t.Context(), 1); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	t.Log("SERVER_SIGNED_MTLS_EXPLICIT_REPLAY_OK")

}
func TestSignedReceiptTrustAndCompleteBinding(t *testing.T) {
	s := openStore(t, config(t, 2))
	it := mustPut(t, s, reading())
	c, k := trust(t)
	good := token(t, receipt(it), c, k)
	for _, mode := range []string{"disabled", "key", "audience", "tenant", "gateway", "unsigned", "tampered", "foreign-key", "oversize", "duplicate-outer", "unknown-outer", "whitespace", "trailing", "invalidutf8", "wrong-domain", "duplicate-signed", "unknown-signed", "case-signed", "version-signed", "keyid-signed", "audience-signed", "receipt-mismatch", "receipt-version", "receipt-policy", "receipt-tenant", "receipt-gateway", "receipt-device", "receipt-event", "receipt-time", "receipt-digest", "new-terminal", "state"} {
		t.Run(mode, func(t *testing.T) {
			tc := c
			b := append([]byte(nil), good...)
			r := receipt(it)
			switch mode {
			case "disabled":
				tc.Enabled = false
			case "key":
				tc.PublicKey = tc.PublicKey[:10]
			case "audience":
				tc.Audience = "other"
			case "tenant":
				tc.TenantID = "other"
			case "gateway":
				tc.GatewayID = "other"
			case "unsigned":
				b, _ = json.Marshal(r)
			case "tampered":
				b = bytes.Replace(b, []byte(`"value"`), []byte(`"wrong"`), 1)
				b[len(b)-5] ^= 1
			case "foreign-key":
				_, key := trust(t)
				b = token(t, r, c, key)
			case "oversize":
				b = bytes.Repeat([]byte("x"), 8193)
			case "duplicate-outer":
				b = append([]byte(`{"signature":"bad",`), b[1:]...)
			case "unknown-outer":
				b = append(b[:len(b)-1], []byte(`,"unknown":true}`)...)
			case "whitespace":
				b = append([]byte(" "), b...)
			case "trailing":
				b = append(b, []byte(`{}`)...)
			case "invalidutf8":
				b = []byte{255}
			case "wrong-domain":
				var w signedWire
				json.Unmarshal(b, &w)
				w.Signature = ed25519.Sign(k, w.Payload)
				b, _ = json.Marshal(w)
			case "receipt-mismatch":
				r.PointID += "x"
				b = token(t, r, c, k)
			case "receipt-version":
				r.Version++
				b = token(t, r, c, k)
			case "receipt-policy":
				r.Policy = "other"
				b = token(t, r, c, k)
			case "receipt-tenant":
				r.TenantID = "other"
				b = token(t, r, c, k)
			case "receipt-gateway":
				r.GatewayID = "other"
				b = token(t, r, c, k)
			case "receipt-device":
				r.DeviceID = "other"
				b = token(t, r, c, k)
			case "receipt-event":
				r.EventID = "other"
				b = token(t, r, c, k)
			case "receipt-time":
				r.ObservedAt = r.ObservedAt.Add(time.Microsecond)
				b = token(t, r, c, k)
			case "receipt-digest":
				r.InputSHA256 = "other"
				b = token(t, r, c, k)
			case "new-terminal":
				r.State = "completed"
				b = token(t, r, c, k)
			case "state":
				r.State = "delivered"
				b = token(t, r, c, k)
			default:
				var w signedWire
				json.Unmarshal(b, &w)
				p := w.Payload
				switch mode {
				case "duplicate-signed":
					p = append([]byte(`{"version":2,`), p[1:]...)
				case "unknown-signed":
					p = append(p[:len(p)-1], []byte(`,"unknown":true}`)...)
				case "case-signed":
					p = bytes.Replace(p, []byte(`"audience"`), []byte(`"AUDIENCE"`), 1)
				case "version-signed":
					p = bytes.Replace(p, []byte(`"version":1`), []byte(`"version":2`), 1)
				case "keyid-signed":
					p = bytes.Replace(p, []byte(`"key_id":"`), []byte(`"key_id":"0`), 1)
				case "audience-signed":
					p = bytes.Replace(p, []byte(c.Audience), []byte("other"), 1)
				}
				b = signedBytes(t, p, k)
			}
			if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, b, tc); ok || e == nil {
				t.Fatal("unsafe deletion", ok, e)
			}
			got, e := s.Next(t.Context(), 2)
			if e != nil || len(got) != 1 {
				t.Fatal("refusal lost input", got, e)
			}
		})
	}
	other := reading()
	other.EventID = "other"
	next := mustPut(t, s, other)
	if ok, e := s.ReconcileSigned(t.Context(), next.Sequence, good, c); ok || !errors.Is(e, ErrEvidence) {
		t.Fatal(ok, e)
	}
	if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, good, c); !ok || e != nil {
		t.Fatal(ok, e)
	}
	if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, good, c); ok || !errors.Is(e, ErrMissing) {
		t.Fatal(ok, e)
	}
	got, e := s.Next(t.Context(), 2)
	if e != nil || len(got) != 1 || got[0].Sequence != next.Sequence {
		t.Fatal(got, e)
	}
}
func TestSignedReceiptRollbackCorruptionAndConcurrentReconcile(t *testing.T) {
	c, k := trust(t)
	s := openStore(t, config(t, 2))
	it := mustPut(t, s, reading())
	b := token(t, receipt(it), c, k)
	if _, e := s.db.Exec(`CREATE TRIGGER refuse_delete BEFORE DELETE ON admission_inputs BEGIN SELECT RAISE(ABORT,'synthetic delete refusal'); END`); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, b, c); ok || e == nil {
		t.Fatal(ok, e)
	}
	got, e := s.Next(t.Context(), 2)
	if e != nil || len(got) != 1 {
		t.Fatal(got, e)
	}
	s.db.Exec(`DROP TRIGGER refuse_delete`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ok, e := s.ReconcileSigned(ctx, it.Sequence, b, c); ok || !errors.Is(e, context.Canceled) {
		t.Fatal(ok, e)
	}
	if _, e = s.db.Exec(`UPDATE admission_inputs SET digest='bad'`); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, b, c); ok || !errors.Is(e, ErrEvidence) {
		t.Fatal(ok, e)
	}
	s.db.Exec(`UPDATE admission_inputs SET digest=?`, it.InputSHA256)
	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, e := s.ReconcileSigned(t.Context(), it.Sequence, b, c)
			if ok {
				mu.Lock()
				n++
				mu.Unlock()
			} else if !errors.Is(e, ErrMissing) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if n != 1 {
		t.Fatal(n)
	}
}
func TestSignedReceiptAtomicDeleteProcessRestart(t *testing.T) {
	if p := os.Getenv("SIGNED_OUTBOX_CHILD_PATH"); p != "" {
		s := openStore(t, Config{Enabled: true, Path: p, Capacity: 2})
		it := mustPut(t, s, reading())
		c, k := trust(t)
		if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, token(t, receipt(it), c, k), c); !ok || e != nil {
			os.Exit(41)
		}
		if e := os.WriteFile(p+".deleted", []byte("committed"), 0600); e != nil {
			os.Exit(42)
		}
		select {}
	}
	c := config(t, 2)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignedReceiptAtomicDeleteProcessRestart$")
	cmd.Env = append(os.Environ(), "SIGNED_OUTBOX_CHILD_PATH="+c.Path)
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, e := os.Stat(c.Path + ".deleted"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	s := openStore(t, c)
	got, e := s.Next(t.Context(), 2)
	if e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
}
func TestSignedTerminalReceiptMeansAdmissionOnly(t *testing.T) {
	for _, state := range []string{"pending", "executing", "outcome_unknown", "completed"} {
		t.Run(state, func(t *testing.T) {
			s := openStore(t, config(t, 1))
			it := mustPut(t, s, reading())
			c, k := trust(t)
			r := receipt(it)
			r.New = false
			r.State = state
			b := token(t, r, c, k)
			if ok, e := s.ReconcileSigned(t.Context(), it.Sequence, b, c); !ok || e != nil {
				t.Fatal(ok, e)
			}
		})
	}
	// No wall clock expiry is inferred. Durable exact admission receipts remain
	// reconcilable after restart. Pin revocation/rotation is a separate trust gate.
}
