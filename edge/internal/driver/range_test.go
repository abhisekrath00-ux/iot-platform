package driver

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRangeReadEnvelopeAndWireWords(t *testing.T) {
	for fn := 1; fn <= 4; fn++ {
		var calls int
		g := &modbusGeneric{read: func(gotFn, offset, count int) ([]byte, error) {
			calls++
			if gotFn != fn || offset != 100 || count != 3 {
				t.Fatal("retargeted request")
			}
			if fn <= 2 {
				return []byte{5}, nil
			}
			return []byte{0x12, 0x34, 0, 2, 0xff, 0xff}, nil
		}}
		words, err := g.ReadRange(context.Background(), fn, 100, 3)
		if err != nil || len(words) != 3 || calls != 1 {
			t.Fatal(words, err, calls)
		}
		if fn <= 2 {
			if words[0] != 1 || words[1] != 0 || words[2] != 1 {
				t.Fatal(words)
			}
		} else if words[0] != 0x1234 || words[1] != 2 || words[2] != 65535 {
			t.Fatal(words)
		}
	}
	for _, r := range [][3]int{{0, 0, 1}, {5, 0, 1}, {6, 0, 1}, {16, 0, 1}, {3, -1, 1}, {3, 65535, 2}, {3, 0, 0}, {3, 0, 33}} {
		g := &modbusGeneric{read: func(int, int, int) ([]byte, error) { t.Fatal("invalid request touched wire"); return nil, nil }}
		if _, err := g.ReadRange(context.Background(), r[0], r[1], r[2]); err == nil {
			t.Fatal("invalid request accepted", r)
		}
	}
}
func TestRangeRejectsWrongByteCountAndCancelledContext(t *testing.T) {
	for _, data := range [][]byte{nil, {1}, {0, 1, 0}, {0, 1, 0, 2, 0, 3}} {
		g := &modbusGeneric{read: func(int, int, int) ([]byte, error) { return data, nil }}
		if _, err := g.ReadRange(context.Background(), 3, 0, 2); err == nil {
			t.Fatal("malformed response accepted", data)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &modbusGeneric{read: func(int, int, int) ([]byte, error) { cancel(); return []byte{0, 1}, nil }}
	if _, err := g.ReadRange(ctx, 3, 0, 1); err == nil {
		t.Fatal("cancelled response accepted")
	}
	g.read = func(int, int, int) ([]byte, error) { t.Fatal("pre-cancelled touched wire"); return nil, nil }
	g.ReadRange(ctx, 3, 0, 1)
}
func TestRangeBusyNeverQueuesAndCloseWaits(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var closed atomic.Bool
	g := &modbusGeneric{read: func(int, int, int) ([]byte, error) {
		close(entered)
		<-release
		if closed.Load() {
			t.Error("closed during read")
		}
		return []byte{0, 1}, nil
	}, close: func() error { closed.Store(true); return nil }}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); g.ReadRange(context.Background(), 3, 0, 1) }()
	<-entered
	if _, err := g.ReadRange(context.Background(), 3, 0, 1); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("concurrent read not refused", err)
	}
	done := make(chan struct{})
	go func() { g.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("closed during transaction")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	<-done
	if !closed.Load() {
		t.Fatal("not closed")
	}
	if _, err := g.ReadRange(context.Background(), 3, 0, 1); err == nil {
		t.Fatal("stale reader reopened closed connection")
	}
	if _, err := g.Poll(context.Background()); err == nil {
		t.Fatal("stale poll reopened closed connection")
	}
	g.Close()
}
