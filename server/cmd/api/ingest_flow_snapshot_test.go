package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestjournal"
	"github.com/jackc/pgx/v5"
)

// Read-only candidate evidence fixture. No journal table, admission, worker or
// effect is installed/executed. A snapshot is NOT complete dependency binding.
func TestIntegrationPublishedFlowSnapshotCandidate(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	tenant := "itest-flow-snapshot"
	seed(t, s, tenant)
	e := ingestjournal.Envelope{EventID: "snapshot-event", TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Value: 42, Quality: "measured", SchemaVersion: 1}
	id := tenant + "-flow"
	v1, v2 := id+"-v1", id+"-v2"
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	definition := `{"trigger":{"device_id":"` + e.DeviceID + `","point_id":"temp","op":">","value":0},"steps":[{"type":"notify","channel_id":"not-enabled-fixture","message":"old"}]}`
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,'Original',$3,'test-user')`, id, tenant, definition); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES($1,$2,$3,1,$4,'published','test-user')`, v1, id, tenant, definition); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=$1 WHERE id=$2`, v1, id); err != nil {
		t.Fatal(err)
	}
	j := ingestjournal.Journal{Pool: s.st.Pool}
	original, h1, err := j.CapturePublishedFlow(ctx, e, id)
	if err != nil {
		t.Fatal(err)
	}
	same, h, err := j.CapturePublishedFlow(ctx, e, id)
	if err != nil || h != h1 || !bytes.Equal(same, original) {
		t.Fatal("stable evidence", err)
	}
	inv, err := ingestjournal.InspectFlowSnapshot(original, h1, e)
	if err != nil || len(inv.Effects) != 1 || inv.Effects[0].Class != "external_notification" {
		t.Fatal(inv, err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET name='Renamed only' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	_, renamedDigest, err := j.CapturePublishedFlow(ctx, e, id)
	if err != nil || renamedDigest == h1 {
		t.Fatal("flow name not bound", err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET name='Original' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// Publish another version and rename. Kept bytes inspect as their OLD config,
	// never silently reloading the latest mutable published pointer/name.
	changed := bytes.ReplaceAll([]byte(definition), []byte("old"), []byte("new"))
	if _, err = s.st.Pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES($1,$2,$3,2,$4,'published','test-user')`, v2, id, tenant, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=$1,name='Renamed' WHERE id=$2`, v2, id); err != nil {
		t.Fatal(err)
	}
	newer, h2, err := j.CapturePublishedFlow(ctx, e, id)
	if err != nil || h2 == h1 || bytes.Equal(newer, original) {
		t.Fatal("new version not bound", err)
	}
	if _, err = ingestjournal.InspectFlowSnapshot(original, h1, e); err != nil {
		t.Fatal("retained original", err)
	}
	if _, err = ingestjournal.InspectFlowSnapshot(newer, h1, e); err == nil {
		t.Fatal("version substitution accepted")
	}
	foreign := e
	foreign.TenantID = "other"
	if _, _, err = j.CapturePublishedFlow(ctx, foreign, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign flow", err)
	}
	wrong := e
	wrong.PointID = "other"
	if _, _, err = j.CapturePublishedFlow(ctx, wrong, id); !errors.Is(err, ingestjournal.ErrSnapshot) {
		t.Fatal("wrong input stream", err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET enabled=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.CapturePublishedFlow(ctx, e, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("disabled flow captured", err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET enabled=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flow_versions SET status='archived' WHERE flow_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.CapturePublishedFlow(ctx, e, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("archived pointer accepted", err)
	}
	foreignTenant := "itest-flow-snapshot-foreign"
	seed(t, s, foreignTenant)
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flow_versions SET status='published',tenant_id=$2 WHERE id=$1`, v2, foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.CapturePublishedFlow(ctx, e, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign version captured", err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flow_versions SET tenant_id=$2 WHERE id=$1`, v2, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET definition=$2 WHERE id=$1`, id, `{"draft":"ignored"}`); err != nil {
		t.Fatal(err)
	}
	// The mutable draft field must not replace the referenced published definition.
	if _, _, err = j.CapturePublishedFlow(ctx, e, id); err != nil {
		t.Fatal("draft contaminated published evidence", err)
	}
	otherID := id + "-other"
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=NULL WHERE id=$1`, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE id=$1`, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,'Other',$3,'test-user')`, otherID, tenant, definition); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES($1,$2,$3,1,$4,'published','test-user')`, otherID+"-v1", otherID, tenant, definition); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=$2 WHERE id=$1`, id, otherID+"-v1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.CapturePublishedFlow(ctx, e, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("different flow version accepted", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = j.CapturePublishedFlow(cancelled, e, id); !errors.Is(err, context.Canceled) {
		t.Fatal("source failure not returned", err)
	}
	var runs int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id=$1`, id).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("candidate executed flow", runs, err)
	}
}
