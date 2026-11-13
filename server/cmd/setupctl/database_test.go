package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/migrate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperatorInspectsWithoutMutationAndRefusesLiveClientsBeforeReviewedApply(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated test PostgreSQL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	name := fmt.Sprintf("setupctl_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+quoted); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	defer func() { admin.Exec(ctx, "DROP DATABASE "+quoted); admin.Close() }()
	parsed, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	parsed.Path = "/" + name
	t.Setenv("DATABASE_URL", parsed.String())
	dir := filepath.Join("..", "..", "migrations")
	absolute, e := filepath.Abs(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("MIGRATIONS_DIR", absolute)
	p, e := pgxpool.New(ctx, parsed.String())
	if e != nil {
		t.Fatal(e)
	}
	baseline := t.TempDir()
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= "0090_" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(baseline, entry.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = migrate.Apply(ctx, p, baseline); e != nil {
		t.Fatal(e)
	}
	output := &bytes.Buffer{}
	if e = run(ctx, []string{"inspect-reviewed"}, strings.NewReader(""), output); e != nil || !strings.Contains(output.String(), "exact_reviewed_baseline_89") {
		t.Fatalf("inspect: %s %v", output, e)
	}
	ack := `{"version":1,"scope":"application_bootstrap","writers_stopped":true,"backup_verified":true}`
	output.Reset()
	if e = run(ctx, []string{"apply-reviewed"}, strings.NewReader(ack), output); e == nil || !strings.Contains(e.Error(), "other database clients") {
		t.Fatalf("active external client was not refused: %v", e)
	}
	var count int
	if e = p.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); e != nil || count != 89 {
		t.Fatal("refused apply mutated ledger", count, e)
	}
	p.Close()
	output.Reset()
	if e = run(ctx, []string{"apply-reviewed"}, strings.NewReader(ack), output); e != nil || !strings.Contains(output.String(), "exact_reviewed_additive_93") {
		t.Fatalf("apply: %s %v", output, e)
	}
	output.Reset()
	if e = run(ctx, []string{"inspect-reviewed"}, strings.NewReader(""), output); e != nil || !strings.Contains(output.String(), "exact_reviewed_additive_93") {
		t.Fatalf("after: %s %v", output, e)
	}
}
