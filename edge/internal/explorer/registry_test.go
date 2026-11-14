package explorer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeReader struct {
	calls            atomic.Int32
	entered, release chan struct{}
}

func (f *fakeReader) ReadRange(context.Context, int, int, int) ([]uint16, error) {
	f.calls.Add(1)
	if f.entered != nil {
		close(f.entered)
		<-f.release
	}
	return []uint16{7}, nil
}
func TestRegistryDetachedLifetimeNeverRetargets(t *testing.T) {
	r := &Registry{}
	f := &fakeReader{}
	remove := r.Add(Connection{Name: "same-name"}, f)
	first := r.Connections()[0].ID
	if _, err := r.Read(context.Background(), Range{first, 3, 0, 1}); err != nil {
		t.Fatal(err)
	}
	remove()
	remove()
	r.Add(Connection{Name: "same-name"}, f)
	if r.Connections()[0].ID == first {
		t.Fatal("lifetime id reused")
	}
	if _, err := r.Read(context.Background(), Range{first, 3, 0, 1}); err == nil {
		t.Fatal("stale id retargeted")
	}
	if f.calls.Load() != 1 {
		t.Fatal("stale id called driver")
	}
}
func TestRegistryRemoveDrainsInFlightBeforeClose(t *testing.T) {
	r := &Registry{}
	f := &fakeReader{entered: make(chan struct{}), release: make(chan struct{})}
	remove := r.Add(Connection{Name: "fixture"}, f)
	id := r.Connections()[0].ID
	done := make(chan struct{})
	go func() { r.Read(context.Background(), Range{id, 3, 0, 1}); close(done) }()
	<-f.entered
	removed := make(chan struct{})
	go func() { remove(); close(removed) }()
	select {
	case <-removed:
		t.Fatal("removed before read drained")
	case <-time.After(10 * time.Millisecond):
	}
	close(f.release)
	<-done
	<-removed
	if len(r.Connections()) != 0 {
		t.Fatal("driver retained")
	}
}
