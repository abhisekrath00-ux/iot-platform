package queue

import (
	"context"
	"testing"
)

func TestStoreForward(t *testing.T) {
	q, err := Open(t.TempDir() + "/q.db")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := q.Put(ctx, "t/t1/g/g1/telemetry", []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	depth, _ := q.Depth(ctx)
	if depth != 5 {
		t.Fatalf("depth=%d, want 5", depth)
	}
	items, err := q.Next(ctx, 3)
	if err != nil || len(items) != 3 {
		t.Fatalf("next: %v len=%d", err, len(items))
	}
	for _, it := range items {
		if err := q.Ack(ctx, it.ID); err != nil {
			t.Fatal(err)
		}
	}
	depth, _ = q.Depth(ctx)
	if depth != 2 {
		t.Fatalf("depth after ack=%d, want 2", depth)
	}
	// FIFO order preserved
	rest, _ := q.Next(ctx, 10)
	if len(rest) != 2 || rest[0].ID >= rest[1].ID {
		t.Fatalf("order broken: %+v", rest)
	}
}

// TestReopenDurability proves the store-and-forward guarantee behind the 72h
// WAN-loss requirement: messages buffered while the uplink is down survive a
// full close/reopen cycle (process restart, power cut) with zero loss and
// original ordering.
func TestReopenDurability(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/q.db"
	ctx := context.Background()

	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 500
	for i := 0; i < n; i++ {
		payload := []byte{byte(i), byte(i >> 8)}
		if err := q.Put(ctx, "telemetry/dev1", payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen as a fresh process would.
	q2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q2.Close()

	depth, err := q2.Depth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != n {
		t.Fatalf("depth after reopen = %d, want %d", depth, n)
	}

	seen := 0
	for {
		items, err := q2.Next(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			want := []byte{byte(seen), byte(seen >> 8)}
			if it.Topic != "telemetry/dev1" || len(it.Payload) != 2 || it.Payload[0] != want[0] || it.Payload[1] != want[1] {
				t.Fatalf("item %d corrupted: %+v", seen, it)
			}
			if err := q2.Ack(ctx, it.ID); err != nil {
				t.Fatal(err)
			}
			seen++
		}
	}
	if seen != n {
		t.Fatalf("replayed %d of %d", seen, n)
	}
	if d, _ := q2.Depth(ctx); d != 0 {
		t.Fatalf("depth after drain = %d", d)
	}
}

func TestOutboxIsBounded(t *testing.T) {
	q, err := Open(t.TempDir() + "/q.db")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	q.MaxRows = 100
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		if err := q.Put(ctx, "t", []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := q.Depth(ctx)
	if n > 100+500 || q.Dropped == 0 {
		t.Fatalf("depth %d dropped %d", n, q.Dropped)
	}
	it, _ := q.Next(ctx, 1)
	if len(it) == 0 || it[0].Payload[0] == 0 {
		t.Fatal("the oldest readings should be the ones dropped")
	}
}
