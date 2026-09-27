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
