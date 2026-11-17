package admissionoutbox

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Invoked only by the server's actual PG-backed handler integration test. The
// fixture contains ephemeral test certificates, not owner keys or enrollment.
func TestLiveAdmissionHTTPFixture(t *testing.T) {
	path := os.Getenv("TEST_LIVE_ADMISSION_HTTP")
	if path == "" {
		t.Skip("live server bridge only")
	}
	var f struct {
		Endpoint                           string
		Roots, Certificate, Key, PublicKey []byte
		Audience                           string
		Reading                            Reading
	}
	b, e := os.ReadFile(path)
	if e != nil || json.Unmarshal(b, &f) != nil {
		t.Fatal("fixture unreadable", e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(f.Roots) {
		t.Fatal("roots")
	}
	cert, e := tls.X509KeyPair(f.Certificate, f.Key)
	if e != nil {
		t.Fatal(e)
	}
	cfg := config(t, 1)
	trust := ReceiptTrust{Enabled: true, Audience: f.Audience, TenantID: f.Reading.TenantID, GatewayID: f.Reading.GatewayID, PublicKey: f.PublicKey}
	session, e := OpenSession(t.Context(), SessionConfig{Enabled: true, Store: cfg, HTTPS: HTTPSConfig{Enabled: true, Endpoint: f.Endpoint, RootCAs: roots, ClientCertificate: cert, Timeout: 5 * time.Second}, Trust: trust, BatchLimit: 1})
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close(t.Context())
	it, e := session.Retain(t.Context(), f.Reading)
	if e != nil {
		t.Fatal(e)
	}
	status, e := session.Status(t.Context())
	if e != nil || status.Capacity == nil || !status.Capacity.Full || status.Capacity.Retained != 1 {
		t.Fatal(status, e)
	}
	result, e := session.SendOnce(t.Context())
	if e == nil || result.Attempted != 1 || result.Reconciled != 0 {
		t.Fatal(result, e)
	}
	status, e = session.Status(t.Context())
	if e != nil || status.LastSend == nil || status.LastSend.Outcome != "uncertain" || !status.Capacity.Full {
		t.Fatal(status, e)
	}
	rows, e := session.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 || rows[0].Sequence != it.Sequence || rows[0].InputSHA256 != it.InputSHA256 {
		t.Fatal("lost retained identity", rows, e)
	}
	// Only a second explicit caller invocation requests the exact committed input.
	result, e = session.SendOnce(t.Context())
	if e != nil || result.Attempted != 1 || result.Reconciled != 1 {
		t.Fatal(result, e)
	}
	rows, e = session.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	status, e = session.Status(t.Context())
	if e != nil || status.LastSend.Outcome != "admission-confirmed" || status.Capacity.Retained != 0 {
		t.Fatal(status, e)
	}
	if e = session.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	status, e = session.Status(t.Context())
	if e != nil || status.State != "closed" || status.Capacity != nil {
		t.Fatal(status, e)
	}
	t.Log("LIVE_SESSION_PG_MTLS_EDGE_REPLAY_OK")
	t.Log("LIVE_HANDLER_PG_MTLS_EDGE_REPLAY_OK")
}
