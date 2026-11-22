package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Isolated reproducer: the fake publisher records acceptance, then loses its
// acknowledgement. No real broker, edge, device or owner account is contacted.
func TestIntegrationConfigPushAcceptedThenPublishErrorDoesNotRearm(t *testing.T) {
	s, _ := testServer(t)
	const tenant = "itest-config-push-ambiguous"
	seed(t, s, tenant)
	if _, err := s.st.Pool.Exec(t.Context(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.st.Pool.Exec(context.Background(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant)
		s.st.Pool.Exec(context.Background(), `DELETE FROM audit_log WHERE tenant_id=$1`, tenant)
	})
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	var accepted atomic.Int32
	s.opsPublishHook = func(_ string, payload []byte) error {
		var envelope configPushEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Error(err)
		}
		if envelope.PushID == "" {
			t.Error("missing exact attempt ID")
		}
		accepted.Add(1)
		return errors.New("synthetic acknowledgement lost after acceptance")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	path := "/v1/gateways/" + tenant + "-gw/config-push"
	first := call(mux, tenant, "admin", "POST", path, `{}`)
	if first.Code != 502 || accepted.Load() != 1 {
		t.Fatalf("first status=%d acceptance=%d", first.Code, accepted.Load())
	}
	// Reading and making the next explicit attempt must not treat a lost broker
	// acknowledgement as evidence that no source reload was accepted.
	observed := call(mux, tenant, "admin", "GET", path, "")
	second := call(mux, tenant, "admin", "POST", path, `{}`)
	if second.Code != 409 || accepted.Load() != 1 {
		t.Fatalf("ambiguous send rearmed: first=%d observed=%s second=%d accepted=%d", first.Code, observed.Body.String(), second.Code, accepted.Load())
	}
}

func configPushUncertaintyFixture(t *testing.T, suffix string) (*server, *http.ServeMux, string, string) {
	t.Helper()
	s, _ := testServer(t)
	tenant := "itest-cp-uncertain-" + suffix
	seed(t, s, tenant)
	if _, err := s.st.Pool.Exec(t.Context(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.st.Pool.Exec(context.Background(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant)
		s.st.Pool.Exec(context.Background(), `DELETE FROM audit_log WHERE tenant_id=$1`, tenant)
	})
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	return s, mux, tenant, "/v1/gateways/" + tenant + "-gw/config-push"
}

func TestIntegrationConfigPushUncertaintyQuarantine(t *testing.T) {
	t.Run("precommitted marker survives API recreation and expiry, receipt resolves", func(t *testing.T) {
		s, m, tenant, path := configPushUncertaintyFixture(t, "restore")
		var id string
		var calls atomic.Int32
		s.opsPublishHook = func(_ string, b []byte) error {
			calls.Add(1)
			var e configPushEnvelope
			if err := json.Unmarshal(b, &e); err != nil {
				t.Fatal(err)
			}
			id = e.PushID
			var detail string
			if err := s.st.Pool.QueryRow(t.Context(), `SELECT detail FROM gateway_config_pushes WHERE id=$1`, id).Scan(&detail); err != nil || detail != configPushPublicationUnknown {
				t.Fatalf("publish preceded durable quarantine: %q %v", detail, err)
			}
			return errors.New("synthetic lost ack")
		}
		if w := call(m, tenant, "admin", "POST", path, `{}`); w.Code != 502 {
			t.Fatal(w.Code)
		}
		if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		// New server instance and new mux: no in-memory lock can explain refusal.
		replacement := &server{st: s.st, secret: s.secret}
		replacement.opsPublishHook = s.opsPublishHook
		fresh := http.NewServeMux()
		fresh.HandleFunc("POST /v1/gateways/{id}/config-push", replacement.pushGatewayConfig)
		fresh.HandleFunc("GET /v1/gateways/{id}/config-push", replacement.listConfigPushes)
		got := call(fresh, tenant, "admin", "GET", path, "")
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"publication_unconfirmed":true`) || !strings.Contains(got.Body.String(), `"status":"sent"`) {
			t.Fatal(got.Code, got.Body.String())
		}
		if w := call(fresh, tenant, "admin", "POST", path, `{}`); w.Code != 409 || calls.Load() != 1 {
			t.Fatalf("restart/expiry rearmed: %d %d", w.Code, calls.Load())
		}
		// Same conditional transitions the existing gateway-scoped ingest uses.
		for _, tr := range [][2]string{{"sent", "applied"}, {"applied", "confirmed"}} {
			tag, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status=$1,detail='synthetic gateway receipt' WHERE id=$2 AND tenant_id=$3 AND gateway_id=$4 AND status=$5`, tr[1], id, tenant, tenant+"-gw", tr[0])
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatal("receipt compatibility", err)
			}
		}
		replacement.opsPublishHook = func(_ string, _ []byte) error { calls.Add(1); return nil }
		w := call(fresh, tenant, "admin", "POST", path, `{}`)
		if w.Code != 202 || calls.Load() != 2 || !strings.Contains(w.Body.String(), `"version":2`) {
			t.Fatal("later explicit Apply after gateway receipt", w.Code, calls.Load(), w.Body.String())
		}
	})
	t.Run("simultaneous admission cannot publish a second version", func(t *testing.T) {
		s, m, tenant, path := configPushUncertaintyFixture(t, "concurrent")
		var calls atomic.Int32
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		s.opsPublishHook = func(_ string, _ []byte) error {
			if calls.Add(1) == 1 {
				close(entered)
			}
			<-release
			return errors.New("synthetic lost ack")
		}
		done := make(chan int, 1)
		go func() { done <- call(m, tenant, "admin", "POST", path, `{}`).Code }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("publish did not start")
		}
		var wg sync.WaitGroup
		codes := make(chan int, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); codes <- call(m, tenant, "admin", "POST", path, `{}`).Code }()
		}
		wg.Wait()
		close(codes)
		for code := range codes {
			if code != 409 {
				t.Error("concurrent admission", code)
			}
		}
		once.Do(func() { close(release) })
		if code := <-done; code != 502 || calls.Load() != 1 {
			t.Fatal(code, calls.Load())
		}
	})
	t.Run("database expiry failure prevents publish", func(t *testing.T) {
		s, m, tenant, path := configPushUncertaintyFixture(t, "sqlfailure")
		var calls atomic.Int32
		s.opsPublishHook = func(_ string, _ []byte) error { calls.Add(1); return nil }
		_, err := s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by,created_at) VALUES('uncertain-old-expiry',$1,$2,1,'devices: []','old','sent','test-user',now()-interval '1 day')`, tenant, tenant+"-gw")
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.st.Pool.Exec(t.Context(), `CREATE FUNCTION h144_refuse_expiry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic expiry refusal'; END $$; CREATE TRIGGER h144_refuse_expiry BEFORE UPDATE ON gateway_config_pushes FOR EACH ROW WHEN (OLD.id='uncertain-old-expiry') EXECUTE FUNCTION h144_refuse_expiry()`)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.st.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS h144_refuse_expiry ON gateway_config_pushes; DROP FUNCTION IF EXISTS h144_refuse_expiry()`)
		})
		w := call(m, tenant, "admin", "POST", path, `{}`)
		if w.Code < 500 || calls.Load() != 0 {
			t.Fatal("SQL failure admitted publication", w.Code, calls.Load())
		}
	})
	t.Run("commit refusal has zero publisher calls", func(t *testing.T) {
		s, m, tenant, path := configPushUncertaintyFixture(t, "commitfailure")
		var calls atomic.Int32
		s.opsPublishHook = func(_ string, _ []byte) error { calls.Add(1); return nil }
		_, err := s.st.Pool.Exec(t.Context(), `CREATE FUNCTION h144_refuse_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic deferred commit refusal'; END $$; CREATE CONSTRAINT TRIGGER h144_refuse_commit AFTER INSERT ON gateway_config_pushes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.tenant_id='itest-cp-uncertain-commitfailure') EXECUTE FUNCTION h144_refuse_commit()`)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.st.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS h144_refuse_commit ON gateway_config_pushes; DROP FUNCTION IF EXISTS h144_refuse_commit()`)
		})
		w := call(m, tenant, "admin", "POST", path, `{}`)
		if w.Code < 500 || calls.Load() != 0 {
			t.Fatal("commit failure admitted publication", w.Code, calls.Load())
		}
	})
	t.Run("post-publish metadata failure retains quarantine", func(t *testing.T) {
		s, m, tenant, path := configPushUncertaintyFixture(t, "clearfailure")
		var calls atomic.Int32
		_, err := s.st.Pool.Exec(t.Context(), `CREATE FUNCTION h144_refuse_clear() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic metadata refusal'; END $$; CREATE TRIGGER h144_refuse_clear BEFORE UPDATE ON gateway_config_pushes FOR EACH ROW WHEN (OLD.tenant_id='itest-cp-uncertain-clearfailure' AND NEW.detail='') EXECUTE FUNCTION h144_refuse_clear()`)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.st.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS h144_refuse_clear ON gateway_config_pushes; DROP FUNCTION IF EXISTS h144_refuse_clear()`)
		})
		s.opsPublishHook = func(_ string, _ []byte) error { calls.Add(1); return nil }
		first := call(m, tenant, "admin", "POST", path, `{}`)
		second := call(m, tenant, "admin", "POST", path, `{}`)
		if first.Code != 502 || second.Code != 409 || calls.Load() != 1 {
			t.Fatal(first.Code, second.Code, calls.Load())
		}
		var detail string
		if err := s.st.Pool.QueryRow(t.Context(), `SELECT detail FROM gateway_config_pushes WHERE tenant_id=$1`, tenant).Scan(&detail); err != nil || detail != configPushPublicationUnknown {
			t.Fatal("quarantine not retained", detail, err)
		}
	})
	t.Run("gateway receipt racing publish error is not downgraded", func(t *testing.T) {
		s, m, tenant, path := configPushUncertaintyFixture(t, "receiptrace")
		s.opsPublishHook = func(_ string, b []byte) error {
			var e configPushEnvelope
			json.Unmarshal(b, &e)
			_, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='applied',detail='synthetic gateway receipt' WHERE id=$1 AND status='sent'`, e.PushID)
			if err != nil {
				t.Error(err)
			}
			return errors.New("synthetic lost ack")
		}
		w := call(m, tenant, "admin", "POST", path, `{}`)
		if w.Code != 502 {
			t.Fatal(w.Code)
		}
		var state, detail string
		if err := s.st.Pool.QueryRow(t.Context(), `SELECT status,detail FROM gateway_config_pushes WHERE tenant_id=$1`, tenant).Scan(&state, &detail); err != nil || state != "applied" || detail != "synthetic gateway receipt" {
			t.Fatal(state, detail, err)
		}
	})
}

// Run against the older handler too when recording rollback/mixed-writer evidence.
// Same schema does NOT imply that an old API understands the quarantine marker.
func TestIntegrationConfigPushQuarantineReadExpiryCompatibility(t *testing.T) {
	s, m, tenant, path := configPushUncertaintyFixture(t, "compatibility")
	var calls atomic.Int32
	s.opsPublishHook = func(_ string, _ []byte) error { calls.Add(1); return nil }
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by,created_at,detail) VALUES('uncertain-old-image-compatibility',$1,$2,1,'devices: []','old','sent','test-user',now()-interval '1 day',$3)`, tenant, tenant+"-gw", configPushPublicationUnknown); err != nil {
		t.Fatal(err)
	}
	observed := call(m, tenant, "admin", "GET", path, "")
	next := call(m, tenant, "admin", "POST", path, `{}`)
	if !strings.Contains(observed.Body.String(), `"status":"sent"`) || next.Code != 409 || calls.Load() != 0 {
		t.Fatalf("old-image/mixed-writer safety not established: GET=%s next=%d publisher_calls=%d", observed.Body.String(), next.Code, calls.Load())
	}
}
