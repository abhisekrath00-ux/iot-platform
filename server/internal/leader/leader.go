// Package leader runs a singleton background job across replicas using a
// Postgres session-level advisory lock: whichever replica holds the lock is
// the leader; if it dies or loses its connection, Postgres releases the lock
// and another replica takes over within one retry interval. No extra
// infrastructure, so it works air-gapped.
package leader

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run blocks until ctx ends. It repeatedly tries to become leader for key;
// while leader it calls job with a context that is cancelled if the lock
// connection fails, so two replicas never run the job concurrently.
func Run(ctx context.Context, pool *pgxpool.Pool, key int64, name string, retry time.Duration, job func(context.Context)) {
	for ctx.Err() == nil {
		if held := runOnce(ctx, pool, key, name, job); !held {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retry):
			}
		}
	}
}

// runOnce tries the lock once; returns true if it was leader (and job ran).
func runOnce(ctx context.Context, pool *pgxpool.Pool, key int64, name string, job func(context.Context)) bool {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil || !got {
		return false
	}
	log.Printf("leader[%s]: acquired", name)
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Watchdog: if the lock connection dies the lock is gone; stop the job.
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				if _, err := conn.Exec(jobCtx, "SELECT 1"); err != nil {
					log.Printf("leader[%s]: lock connection lost, stepping down: %v", name, err)
					cancel()
					return
				}
			}
		}
	}()
	job(jobCtx)
	// Explicit unlock on a fresh context so a cancelled ctx still releases.
	conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key) //nolint:errcheck
	log.Printf("leader[%s]: released", name)
	return true
}
