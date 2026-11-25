package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyPreparationRefusalsAndRedaction(t *testing.T) {
	base := t.TempDir()
	os.Chmod(base, 0700)
	dir := filepath.Join(base, "store")
	os.Mkdir(dir, 0700)
	binding := filepath.Join(base, "binding.json")
	// Missing files must not trigger provisioning or reveal supplied path/DSN.
	r := inspect(context.Background(), "postgres://private-user:private-password@private-host/db", dir, binding, "fixture")
	if !r.ReviewOnly || r.PreparedFilesVerified || r.ActivationAllowed || r.ServicesChanged || strings.Contains(r.Reason, "private-") {
		t.Fatal(r)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("provisioned storage")
	}
	if _, err := os.Stat(binding); !os.IsNotExist(err) {
		t.Fatal("created binding")
	}
	if err := applyadmission.PrepareNewDeploymentBinding(dir, binding, "fixture"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(binding)
	r = inspect(context.Background(), "", dir, binding, "fixture")
	after, _ := os.ReadFile(binding)
	if !r.PreparedFilesVerified || r.NewInstallObservation || r.ActivationAllowed || r.ServicesChanged || !bytes.Equal(before, after) {
		t.Fatal(r)
	}
	var out, errors bytes.Buffer
	code := run([]string{"--store", dir, "--binding", binding, "--deployment", "fixture", "--json"}, func(string) string { return "" }, &out, &errors)
	if code != 1 || strings.Contains(out.String(), base) || !strings.Contains(out.String(), `"activation_allowed":false`) {
		t.Fatal(code, out.String(), errors.String())
	}
	out.Reset()
	if code := run([]string{"--activate"}, func(string) string { return "" }, &out, &errors); code != 2 {
		t.Fatal("accepted activation", code)
	}
}

func TestIntegrationPreparationReadOnlyDatabase(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := fmt.Sprintf("apply_preparation_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(t.Context(), "CREATE SCHEMA "+quoted+";CREATE TABLE "+quoted+".gateway_config_pushes (LIKE public.gateway_config_pushes INCLUDING ALL); SET search_path TO "+quoted+",public"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	guard, err := os.ReadFile("../../internal/applyadmission/candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), string(guard)); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	base := t.TempDir()
	os.Chmod(base, 0700)
	directory := filepath.Join(base, "store")
	os.Mkdir(directory, 0700)
	binding := filepath.Join(base, "expected.json")
	if err = applyadmission.PrepareNewDeploymentBinding(directory, binding, "fixture"); err != nil {
		t.Fatal(err)
	}
	r := inspect(t.Context(), u.String(), directory, binding, "fixture")
	if !r.NewInstallObservation || r.ActivationAllowed || r.ServicesChanged {
		t.Fatal(r)
	}
	// Terminal legacy history must never be silently adopted into empty storage.
	if _, err = conn.Exec(t.Context(), "ALTER TABLE gateway_config_pushes DISABLE TRIGGER apply_attempt_guard_candidate; INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES('old','t','g',1,'devices: []','digest','confirmed','u'); ALTER TABLE gateway_config_pushes ENABLE TRIGGER apply_attempt_guard_candidate"); err != nil {
		t.Fatal(err)
	}
	r = inspect(t.Context(), u.String(), directory, binding, "fixture")
	if r.NewInstallObservation || !r.PreparedFilesVerified || r.ActivationAllowed {
		t.Fatal(r)
	}
	var n int
	if err = conn.QueryRow(t.Context(), "SELECT count(*) FROM gateway_config_pushes").Scan(&n); err != nil || n != 1 {
		t.Fatal("changed DB", n, err)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Fatal("changed store", len(entries))
	}
}
