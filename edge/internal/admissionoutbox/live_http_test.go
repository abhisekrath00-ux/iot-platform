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
	exchange, e := NewHTTPSExchange(HTTPSConfig{Enabled: true, Endpoint: f.Endpoint, RootCAs: roots, ClientCertificate: cert, Timeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	s := openStore(t, config(t, 1))
	it := mustPut(t, s, f.Reading)
	trust := ReceiptTrust{Enabled: true, Audience: f.Audience, TenantID: f.Reading.TenantID, GatewayID: f.Reading.GatewayID, PublicKey: f.PublicKey}
	p, e := NewPublisher(PublisherConfig{Enabled: true, Store: s, Trust: trust, Exchange: exchange, BatchLimit: 1})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close(t.Context())
	result, e := p.RunOnce(t.Context())
	if e == nil || result.Attempted != 1 || result.Reconciled != 0 {
		t.Fatal(result, e)
	}
	rows, e := s.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 || rows[0].Sequence != it.Sequence || rows[0].InputSHA256 != it.InputSHA256 {
		t.Fatal("lost retained identity", rows, e)
	}
	// Only a second explicit caller invocation requests the exact committed input.
	result, e = p.RunOnce(t.Context())
	if e != nil || result.Attempted != 1 || result.Reconciled != 1 {
		t.Fatal(result, e)
	}
	rows, e = s.Next(t.Context(), 1)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	t.Log("LIVE_HANDLER_PG_MTLS_EDGE_REPLAY_OK")
}
