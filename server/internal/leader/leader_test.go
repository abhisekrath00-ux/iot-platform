package leader

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Needs a Postgres (TEST_DATABASE_URL); skipped otherwise.
func TestSingleLeaderAndFailover(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var running, maxRunning atomic.Int32
	job := func(ctx context.Context) {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		<-ctx.Done()
		running.Add(-1)
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go Run(ctxA, pool, 987654321, "a", 50*time.Millisecond, job)
	go Run(ctxB, pool, 987654321, "b", 50*time.Millisecond, job)

	time.Sleep(600 * time.Millisecond)
	if running.Load() != 1 {
		t.Fatalf("running = %d, want exactly 1 leader", running.Load())
	}
	// Stop whichever is leader: A is not guaranteed to be it, so stop both
	// contenders one at a time and require the survivor to take over.
	cancelA()
	time.Sleep(600 * time.Millisecond)
	if running.Load() != 1 {
		t.Fatalf("after A stopped running = %d, want 1 (B took over or stayed)", running.Load())
	}
	if maxRunning.Load() != 1 {
		t.Fatalf("two leaders ran concurrently (max %d)", maxRunning.Load())
	}
	cancelB()
	time.Sleep(300 * time.Millisecond)
	if running.Load() != 0 {
		t.Fatalf("job still running after all contenders stopped: %d", running.Load())
	}
}
