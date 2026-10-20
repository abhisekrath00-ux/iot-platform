package main

// Regression guard for the migration ledger: once a database is current,
// migrate must skip every file instead of re-running no-op DDL. A no-op
// ALTER TABLE still takes ACCESS EXCLUSIVE, and a re-migration pass could
// deadlock against an in-flight request (see docs/status.md, 2026-10-20
// migration ledger entry). If this test slows down or fails, DDL is being
// re-applied on an already-migrated database.
import (
	"context"
	"os"
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
