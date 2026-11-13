package leader

import (
	"context"
	"testing"
	"time"
)

func waitSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not complete")
	}
}

func TestWatchdogBoundsBlockedPingAndCancelsJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	exited := make(chan struct{})
	done := watchLock(ctx, cancel, "bounded-test", time.Millisecond, 25*time.Millisecond, func(c context.Context) error {
		close(started)
		<-c.Done()
		close(exited)
		return c.Err()
	})
	waitSignal(t, started)
	waitSignal(t, done)
	waitSignal(t, exited)
	if ctx.Err() == nil {
		t.Fatal("failed ping did not cancel cooperative job")
	}
}

func TestWatchdogJoinWaitsForCancelledPing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := watchLock(ctx, cancel, "join-test", time.Millisecond, time.Second, func(c context.Context) error {
		close(started)
		<-c.Done()
		close(cancelled)
		<-release
		return c.Err()
	})
	waitSignal(t, started)
	cancel()
	waitSignal(t, cancelled)
	select {
	case <-done:
		t.Fatal("joined before ping returned; connection reuse would race")
	default:
	}
	close(release)
	waitSignal(t, done)
}

func TestWatchdogCancellationBeforeTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := watchLock(ctx, cancel, "cancelled-test", time.Hour, time.Second, func(context.Context) error {
		t.Error("ping ran after pre-cancel")
		return nil
	})
	waitSignal(t, done)
}
