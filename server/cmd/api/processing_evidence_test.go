package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestprocessing"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/processingschema"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProcessingEvidenceAccessAndInput(t *testing.T) {
	s := &server{}
	h := http.HandlerFunc(s.processingEvidence)
	for _, role := range []string{"viewer", "operator", "installer", "gateway", ""} {
		if w := call(h, "a", role, "GET", "/", ""); w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	for _, key := range []string{string(auth.CtxViaKey), "customer"} {
		r := httptest.NewRequest("GET", "/", nil)
		ctx := context.WithValue(r.Context(), auth.CtxRole, "admin")
		if key == string(auth.CtxViaKey) {
			ctx = context.WithValue(ctx, auth.CtxViaKey, true)
		} else {
			ctx = context.WithValue(ctx, scopeKey{}, "x")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(ctx))
		if w.Code != 403 {
			t.Fatal(key, w.Code)
		}
	}
	for _, q := range []string{"", "event_id=x", "event_id=x&observed_at=tomorrow", "event_id=x&observed_at=2026-11-15T00:00:00.1234567Z", "event_id=x&event_id=y&observed_at=2026-11-15T00:00:00Z", "event_id=x&observed_at=2026-11-15T00:00:00Z&execute=true", "event_id=%00&observed_at=2026-11-15T00:00:00Z"} {
		w := call(h, "a", "admin", "GET", "/?"+q, "")
		if w.Code != 400 {
			t.Fatal(q, w.Code, w.Body)
		}
	}
}
func TestIntegrationProcessingEvidenceReadOnly(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated Postgres required")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("processing_view_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 2
	cfg.MinConns = 0
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Close()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err = admin.Exec(c, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	}()
	s := &server{st: &store.Store{Pool: p}}
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	if err = migrate(ctx, s.st); err != nil {
		t.Fatal(err)
	}
	seed(t, s, "evidence-a")
	seed(t, s, "evidence-b")
	h := http.HandlerFunc(s.processingEvidence)
	at := time.Now().UTC().Truncate(time.Microsecond)
	path := "/v1/system/processing-evidence?" + url.Values{"event_id": {"synthetic-input"}, "observed_at": {at.Format(time.RFC3339Nano)}}.Encode()
	w := call(h, "evidence-a", "admin", "GET", path, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "staged_store_absent") {
		t.Fatal(w.Code, w.Body)
	}
	// One operator connection only for the separately reviewed staged apply.
	p.Reset()
	if _, err = processingschema.Apply(ctx, p, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	// Retained destinations/message bodies must not be returned by this view.
	if _, err = p.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('evidence-channel','evidence-a','email','PRIVATE_DESTINATION@example.invalid',true);
 INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('evidence-flow','evidence-a','Synthetic','{}','test-user');
 INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('evidence-version','evidence-flow','evidence-a',1,'{"trigger":{"device_id":"evidence-a-dev","point_id":"temp","op":">","value":1},"steps":[{"type":"notify","channel_id":"evidence-channel","message":"PRIVATE_MESSAGE_BODY"}]}','published','test-user');
 UPDATE flows SET published_version_id='evidence-version' WHERE id='evidence-flow'`); err != nil {
		t.Fatal(err)
	}
	engine := ingestprocessing.Engine{Pool: p, Enabled: true}
	e := ingestprocessing.Envelope{EventID: "synthetic-input", TenantID: "evidence-a", GatewayID: "evidence-a-gw", DeviceID: "evidence-a-dev", PointID: "temp", ObservedAt: at, Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
	if _, err = engine.Admit(ctx, e); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"evidence-b", "missing"} {
		w = call(h, tenant, "admin", "GET", path, "")
		if w.Code != 404 || !strings.Contains(w.Body.String(), "does not prove") {
			t.Fatal(tenant, w.Code, w.Body)
		}
	}
	verify := func(state, quarantine string) {
		t.Helper()
		w = call(h, "evidence-a", "admin", "GET", path, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body)
		}
		var got map[string]any
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || got["state"] != state || got["quarantine"] != quarantine || got["read_only"] != true || got["execution_available"] != false {
			t.Fatal(w.Body)
		}
		for _, field := range []string{"plan", "envelope", "target", "message", "definition", "value"} {
			if _, ok := got[field]; ok {
				t.Fatal("private/unneeded retained field", field)
			}
		}
	}
	verify("pending", "")
	if strings.Contains(w.Body.String(), "PRIVATE_") || !strings.Contains(w.Body.String(), `"intent_count":1`) {
		t.Fatal("retained details leaked or missing real occurrence", w.Body)
	}
	// A second immutable admission exposes a real unsupported-history reason,
	// without turning this read into claim, send, repair or takeover.
	if _, err = p.Exec(ctx, `UPDATE flow_versions SET definition=jsonb_set(definition,'{cooldown_seconds}','60'::jsonb) WHERE id='evidence-version'`); err != nil {
		t.Fatal(err)
	}
	blocked := e
	blocked.EventID = "synthetic-blocked"
	if _, err = engine.Admit(ctx, blocked); err != nil {
		t.Fatal(err)
	}
	blockedPath := "/v1/system/processing-evidence?" + url.Values{"event_id": {blocked.EventID}, "observed_at": {at.Format(time.RFC3339Nano)}}.Encode()
	bw := call(h, "evidence-a", "admin", "GET", blockedPath, "")
	if bw.Code != 200 || !strings.Contains(bw.Body.String(), "unsupported_plan_stage") || !strings.Contains(bw.Body.String(), "ordered_history_stage_required") || strings.Contains(bw.Body.String(), "PRIVATE_") {
		t.Fatal(bw.Code, bw.Body)
	}
	if _, err = p.Exec(ctx, `UPDATE ingest_processing_v1 SET state='executing',attempt_id='12345678-1234-1234-1234-123456789012' WHERE event_id=$1`, e.EventID); err != nil {
		t.Fatal(err)
	}
	verify("executing", "attempt_started_no_takeover_or_retry")
	if _, err = p.Exec(ctx, `UPDATE ingest_processing_v1 SET state='outcome_unknown' WHERE event_id=$1`, e.EventID); err != nil {
		t.Fatal(err)
	}
	verify("outcome_unknown", "attempt_outcome_unknown_no_retry")
	if _, err = p.Exec(ctx, `UPDATE ingest_processing_v1 SET state='completed' WHERE event_id=$1`, e.EventID); err != nil {
		t.Fatal(err)
	}
	verify("completed", "")
	// A canceled/unreadable DB check never turns into missing/delivered evidence.
	req := httptest.NewRequest("GET", path, nil)
	requestCtx := context.WithValue(req.Context(), auth.CtxRole, "admin")
	requestCtx = context.WithValue(requestCtx, auth.CtxTenant, "evidence-a")
	requestCtx, cancel := context.WithCancel(requestCtx)
	cancel()
	unavailable := httptest.NewRecorder()
	h.ServeHTTP(unavailable, req.WithContext(requestCtx))
	if unavailable.Code != 503 || strings.Contains(unavailable.Body.String(), "synthetic-input") {
		t.Fatal(unavailable.Code, unavailable.Body)
	}

	var state string
	var count int
	if err = p.QueryRow(ctx, `SELECT state FROM ingest_processing_v1 WHERE event_id=$1`, e.EventID).Scan(&state); err != nil || state != "completed" {
		t.Fatal(state, err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id=$1`, e.EventID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = p.Exec(ctx, `UPDATE ingest_processing_v1 SET plan_sha256=decode(repeat('00',32),'hex') WHERE event_id=$1`, e.EventID); err != nil {
		t.Fatal(err)
	}
	w = call(h, "evidence-a", "admin", "GET", path, "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "evidence_integrity_unknown") || strings.Contains(w.Body.String(), "synthetic-input") {
		t.Fatal(w.Code, w.Body)
	}
}
