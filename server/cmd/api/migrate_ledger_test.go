package main

// Regression guard for the migration ledger: once a database is current,
// migrate must skip every file instead of re-running no-op DDL. A no-op
// ALTER TABLE still takes ACCESS EXCLUSIVE, and a re-migration pass could
// deadlock against an in-flight request (see docs/status.md, 2026-10-20
// migration ledger entry). If this test slows down or fails, DDL is being
// re-applied on an already-migrated database.
import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	migratepkg "github.com/abhisekrath00-ux/iot-platform/server/internal/migrate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

func TestSecondMigrateTakesNoDDLLocks(t *testing.T) {
	st, err := store.Connect(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// 5 back-to-back migrate passes against the current DB; each should be near-instant.
	for i := 0; i < 5; i++ {
		start := time.Now()
		if err := migratepkg.Apply(context.Background(), st.Pool, "../../migrations"); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("migrate pass %d took %v - DDL is not being skipped", i, d)
		}
	}
}

// newMigrateTestDB creates a fresh, uniquely named database on the test
// server so migration lifecycle tests never touch the shared test database.
func newMigrateTestDB(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	// same server, maintenance database: scheme://host[:port]/postgres?params
	slash := strings.LastIndex(url, "/")
	base, params := url[:slash+1], ""
	if q := strings.Index(url[slash:], "?"); q >= 0 {
		params = url[slash:][q:]
	}
	admin, err := store.Connect(ctx, base+"postgres"+params)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("hexmon_mig_%d", time.Now().UnixNano())
	if _, err := admin.Pool.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a2, err := store.Connect(context.Background(), base+"postgres"+params)
		if err != nil {
			return
		}
		defer a2.Close()
		a2.Pool.Exec(context.Background(), `DROP DATABASE `+name)
	})
	return base + name + params
}

// An existing pre-ledger database (all files applied, no schema_migrations
// table) upgrades cleanly: the ledgered pass re-applies every idempotent
// file once, records them all, and takes the no-lock path from then on.
func TestIntegrationMigrateUpgradesPreLedgerDB(t *testing.T) {
	url := newMigrateTestDB(t)
	ctx := context.Background()
	st, err := store.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Simulate a pre-ledger install: apply every file directly, no ledger.
	entries, _ := os.ReadDir("../../migrations")
	files := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile("../../migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, string(b)); err != nil {
			t.Fatalf("pre-ledger apply %s: %v", e.Name(), err)
		}
		files++
	}
	if _, err := st.Pool.Exec(ctx, `DROP TABLE IF EXISTS schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := migratepkg.Apply(ctx, st.Pool, "../../migrations"); err != nil {
		t.Fatalf("upgrade migrate failed: %v", err)
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != files {
		t.Fatalf("ledger has %d files, want %d", n, files)
	}
}

// A crash between a file's apply and its ledger write (or a lost ledger row)
// re-runs just that file on the next boot, which must succeed.
func TestIntegrationMigrateRecoversAfterLedgerRowLoss(t *testing.T) {
	url := newMigrateTestDB(t)
	ctx := context.Background()
	st, err := store.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := migratepkg.Apply(ctx, st.Pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM schema_migrations WHERE filename='0074_geofence_alerts.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := migratepkg.Apply(ctx, st.Pool, "../../migrations"); err != nil {
		t.Fatalf("re-apply after ledger loss failed: %v", err)
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE filename='0074_geofence_alerts.sql'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ledger row not restored: n=%d err=%v", n, err)
	}
}

// Two replicas booting against an empty database at once both succeed:
// the advisory lock serializes them and the second skips what the first did.
func TestIntegrationMigrateConcurrentFirstBoots(t *testing.T) {
	url := newMigrateTestDB(t)
	ctx := context.Background()
	st, err := store.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- migratepkg.Apply(ctx, st.Pool, "../../migrations") }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent boot: %v", err)
		}
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir("../../migrations")
	files := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files++
		}
	}
	if n != files {
		t.Fatalf("ledger has %d files, want %d", n, files)
	}
}

// The singleton-job advisory lock keys must be distinct: leader.Run holds its
// key for the whole job, so two loops sharing a key means one of them never
// runs. leaderFlowScheduler and leaderIndexSink collided on 0x...4F51, which
// would have starved the index sink on any deployment that enabled it.
func TestLeaderKeysDistinct(t *testing.T) {
	keys := map[int64]string{
		leaderReportScheduler: "report-scheduler",
		leaderFlowScheduler:   "flow-scheduler",
		leaderKPIRules:        "kpi-rules",
		leaderAutoCommands:    "auto-commands",
		leaderIndexSink:       "index-sink",
		leaderRetention:       "retention",
	}
	if len(keys) != 6 {
		names := []string{keys[leaderReportScheduler], keys[leaderFlowScheduler], keys[leaderKPIRules], keys[leaderAutoCommands], keys[leaderIndexSink], keys[leaderRetention]}
		t.Fatalf("leader advisory keys collide: %v", names)
	}
}
