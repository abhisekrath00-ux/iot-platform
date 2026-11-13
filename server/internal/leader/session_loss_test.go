package leader

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The real lock session disappears while its process and cooperative callback
// remain alive. This is different from SIGKILL, and is not an effect-fencing test.
func TestLockSessionLossCancelsAndJoinsCooperativeJob(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	app := fmt.Sprintf("leader-loss-%d", time.Now().UnixNano())
	cfg.ConnConfig.RuntimeParams["application_name"] = app
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	observer, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	key := time.Now().UnixNano()
	started := make(chan context.Context, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runOnce(ctx, pool, key, "session-loss-test", func(jobCtx context.Context) { started <- jobCtx; <-jobCtx.Done() })
	}()
	// Join on all failure paths before closing its pool. Never strand a callback.
	defer func() { cancel(); waitSignal(t, done) }()
	var jobCtx context.Context
	select {
	case jobCtx = <-started:
	case <-ctx.Done():
		t.Fatal("job did not start")
	}
	var pid int32
	if err := observer.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE application_name=$1", app).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	var killed bool
	if err := observer.QueryRow(ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&killed); err != nil || !killed {
		t.Fatalf("terminate exact fixture backend: %v / %v", killed, err)
	}
	takeover, err := observer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer takeover.Release()
	var acquired bool
	for !acquired {
		if err := takeover.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if !acquired {
			select {
			case <-time.After(10 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal("lock never released")
			}
		}
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if _, err := takeover.Exec(c, "SELECT pg_advisory_unlock($1)", key); err != nil {
			_ = takeover.Conn().Close(c)
		}
	}()
	// Do not assert an overlap duration or a timing-dependent no-overlap claim.
	// Either state is possible under scheduling; cancellation is not fencing.
	t.Logf("new holder after %v; old cooperative job already cancelled=%v; no external effects exercised", time.Since(began), jobCtx.Err() != nil)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("cooperative job did not cancel/join after session loss")
	}
	if jobCtx.Err() == nil {
		t.Fatal("job returned without cancellation after session loss")
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool did not recover after dead session: %v", err)
	}
}
