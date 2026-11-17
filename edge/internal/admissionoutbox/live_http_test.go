package admissionoutbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
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
	sessionCfg := SessionConfig{Enabled: true, Store: cfg, HTTPS: HTTPSConfig{Enabled: true, Endpoint: f.Endpoint, RootCAs: roots, ClientCertificate: cert, Timeout: 5 * time.Second}, Trust: trust, BatchLimit: 1}
	session, e := OpenSession(t.Context(), sessionCfg)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close(t.Context())
	source := collectorLoopbackSource(t, f.Reading)
	collectorCfg := CollectorConfig{Enabled: true, Session: session, Reader: source, DeviceID: f.Reading.DeviceID, Points: map[string]string{f.Reading.PointID: f.Reading.Unit}}
	journalCfg := journalConfig(t)
	collector, e := NewJournalCollector(t.Context(), collectorCfg, journalCfg)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = session.store.db.Exec(`CREATE TRIGGER live_retention_fault BEFORE INSERT ON admission_inputs BEGIN SELECT RAISE(ABORT,'synthetic retention refusal after journal'); END`); e != nil {
		t.Fatal(e)
	}
	if r, e := collector.PollOnce(t.Context()); e == nil || r.Retained != 0 || r.Pending != 1 || collector.Status().PendingDurability != "journal-committed" {
		t.Fatal(r, e, collector.Status())
	}
	frozen := collector.pending[0]
	if e = collector.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = session.store.db.Exec("DROP TRIGGER live_retention_fault"); e != nil {
		t.Fatal(e)
	}
	if e = session.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	session, e = OpenSession(t.Context(), sessionCfg)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close(t.Context())
	collectorCfg.Session = session
	collectorCfg.Reader = pollFunc(func(context.Context) ([]driver.Reading, error) {
		t.Fatal("journal recovery polled source")
		return nil, errors.New("forbidden")
	})
	collector, e = NewJournalCollector(t.Context(), collectorCfg, journalCfg)
	if e != nil {
		t.Fatal(e)
	}
	defer collector.Close(t.Context())
	if len(collector.pending) != 1 || collector.pending[0] != frozen {
		t.Fatal("changed journal input")
	}
	if r, e := collector.RetainPending(t.Context()); e != nil || r.Retained != 1 || r.Pending != 0 {
		t.Fatal(r, e)
	}
	if e = collector.Stop(t.Context()); e != nil {
		t.Fatal(e)
	}
	retained, e := session.store.Next(t.Context(), 1)
	if e != nil || len(retained) != 1 {
		t.Fatal(retained, e)
	}
	it := retained[0]
	evidence, e := json.Marshal(it.Reading)
	if e != nil {
		t.Fatal(e)
	}
	collectedPath := os.Getenv("TEST_COLLECTED_READING_PATH")
	if collectedPath == "" {
		t.Fatal("missing synthetic collected evidence path")
	}
	if e = os.WriteFile(collectedPath, evidence, 0600); e != nil {
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
	t.Log("LIVE_JOURNALED_COLLECTOR_PG_MTLS_EDGE_REPLAY_OK")
	t.Log("LIVE_COLLECTOR_SESSION_PG_MTLS_EDGE_REPLAY_OK")
	t.Log("LIVE_SESSION_PG_MTLS_EDGE_REPLAY_OK")
	t.Log("LIVE_HANDLER_PG_MTLS_EDGE_REPLAY_OK")
}
