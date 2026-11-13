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
// while leader it calls job with a context that is cancelled after the lock
// connection fails. Detection is periodic and cancellation is cooperative: a new
// holder can start before the old callback stops. Jobs need their own fencing
// or idempotency for external effects; this is not an exactly-once guarantee.
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
	// pgx connections are not safe for simultaneous operations. Stop and join
	// the watchdog before unlocking or returning its connection to the pool.
	watchDone := watchLock(jobCtx, cancel, name, 5*time.Second, 2*time.Second, func(pingCtx context.Context) error {
		_, err := conn.Exec(pingCtx, "SELECT 1")
		return err
	})
	defer func() {
		cancel()
		<-watchDone
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer unlockCancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			// Never pool a connection whose session lock might still be held.
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer closeCancel()
			_ = conn.Conn().Close(closeCtx)
		}
		log.Printf("leader[%s]: released", name)
	}()
	job(jobCtx)
	return true
}

// watchLock bounds each check and closes done only after its last use of the
// connection. ping must honor context cancellation, as pgx Exec does.
func watchLock(ctx context.Context, cancel context.CancelFunc, name string, interval, timeout time.Duration, ping func(context.Context) error) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pingCtx, stop := context.WithTimeout(ctx, timeout)
				err := ping(pingCtx)
				stop()
				if err != nil {
					log.Printf("leader[%s]: lock connection lost, stepping down: %v", name, err)
					cancel()
					return
				}
			}
		}
	}()
	return done
}
