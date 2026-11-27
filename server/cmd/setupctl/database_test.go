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

func TestReviewedApplyOnAlreadyRecordedLedgerIsAcknowledgedNoOp(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated test PostgreSQL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	name := fmt.Sprintf("setupctl_repeat_test_%d", time.Now().UnixNano())
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
	p.Close()
	ack := `{"version":1,"scope":"application_bootstrap","writers_stopped":true,"backup_verified":true}`
	output := &bytes.Buffer{}
	if e = run(ctx, []string{"apply-reviewed"}, strings.NewReader(ack), output); e != nil || !strings.Contains(output.String(), "exact_reviewed_additive_93") {
		t.Fatalf("first apply: %s %v", output, e)
	}
	// Record the reviewed 93 ledger and a retained application row before the
	// repeat apply; the repeat must not replay, skip or rewrite any of it.
	p2, e := pgxpool.New(ctx, parsed.String())
	if e != nil {
		t.Fatal(e)
	}
	rows, e := p2.Query(ctx, "SELECT filename FROM schema_migrations ORDER BY filename")
	if e != nil {
		t.Fatal(e)
	}
	var recorded []string
	for rows.Next() {
		var filename string
		if e = rows.Scan(&filename); e != nil {
			t.Fatal(e)
		}
		recorded = append(recorded, filename)
	}
	rows.Close()
	if e = rows.Err(); e != nil || len(recorded) != 93 {
		t.Fatalf("recorded ledger: %d %v", len(recorded), e)
	}
	if _, e = p2.Exec(ctx, "INSERT INTO tenants (id, name) VALUES ('setupctl_repeat_retained', 'retained marker')"); e != nil {
		t.Fatal(e)
	}
	p2.Close()
	// A repeat apply without the exact acknowledgement refuses, even though
	// nothing would change: acknowledgement is per-run, never cached.
	output.Reset()
	if e = run(ctx, []string{"apply-reviewed"}, strings.NewReader(""), output); e == nil || !strings.Contains(e.Error(), "acknowledgement required") {
		t.Fatalf("unacknowledged repeat apply was not refused: %v", e)
	}
	// The acknowledged repeat apply is a verified no-op: ApplyReviewed records
	// nothing new, and inspection still proves the exact reviewed 93 ledger.
	output.Reset()
	if e = run(ctx, []string{"apply-reviewed"}, strings.NewReader(ack), output); e != nil || !strings.Contains(output.String(), "exact_reviewed_additive_93") {
		t.Fatalf("repeat apply: %s %v", output, e)
	}
	p3, e := pgxpool.New(ctx, parsed.String())
	if e != nil {
		t.Fatal(e)
	}
	defer p3.Close()
	rows, e = p3.Query(ctx, "SELECT filename FROM schema_migrations ORDER BY filename")
	if e != nil {
		t.Fatal(e)
	}
	var after []string
	for rows.Next() {
		var filename string
		if e = rows.Scan(&filename); e != nil {
			t.Fatal(e)
		}
		after = append(after, filename)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if len(after) != 93 {
		t.Fatalf("repeat apply changed ledger size: %d", len(after))
	}
	for i := range recorded {
		if recorded[i] != after[i] {
			t.Fatalf("repeat apply rewrote ledger entry %d: %s -> %s", i, recorded[i], after[i])
		}
	}
	var retained string
	if e = p3.QueryRow(ctx, "SELECT name FROM tenants WHERE id = 'setupctl_repeat_retained'").Scan(&retained); e != nil || retained != "retained marker" {
		t.Fatalf("retained row lost after repeat apply: %q %v", retained, e)
	}
}
