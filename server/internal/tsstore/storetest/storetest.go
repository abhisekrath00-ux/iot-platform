// Package storetest is the conformance suite every tsstore.Store must pass.
// An adapter's test supplies the store plus a Loader that writes raw samples
// into the same backend.
package storetest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore"
)

// Loader writes one raw sample into the backend under test. Samples for
// distinct (tenant, device, point) must not affect each other.
type Loader interface {
	Add(ctx context.Context, tenant, device, point string, at time.Time, value float64) error
	Reset(ctx context.Context, tenants ...string) error
}

// Run checks aggregation correctness, ordering, tenant isolation and the
// empty-window contract. Tenants "conf-a" and "conf-b" are reset first.
func Run(t *testing.T, s tsstore.Store, l Loader) {
	t.Helper()
	ctx := context.Background()
	if err := l.Reset(ctx, "conf-a", "conf-b"); err != nil {
		t.Fatal(err)
	}
	// Samples sit 1-3 minutes after the previous UTC hour start, which is the same
	// hour bucket in any whole- or half-hour timezone offset except when the offset
	// is 30 minutes (then the bucket still contains all three).
	hr := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	for i, v := range []float64{10, 20, 60} {
		if err := l.Add(ctx, "conf-a", "dev", "kw", hr.Add(time.Duration(i+1)*time.Minute), v); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Add(ctx, "conf-b", "dev", "kw", hr.Add(5*time.Minute), 1000); err != nil {
		t.Fatal(err)
	}

	t.Run("hour bucket math", func(t *testing.T) {
		bs, err := s.Aggregate(ctx, tsstore.SeriesQuery{Tenant: "conf-a", DeviceID: "dev", PointID: "kw", WindowHours: 6, GroupBy: "hour"})
		if err != nil || len(bs) != 1 {
			t.Fatalf("buckets=%v err=%v", bs, err)
		}
		b := bs[0]
		if b.Count != 3 || b.Min != 10 || b.Max != 60 || math.Abs(b.Sum-90) > 1e-9 || math.Abs(b.Avg-30) > 1e-9 {
			t.Fatalf("bad bucket %+v", b)
		}
		// Buckets follow the database session timezone, which can be offset by
		// 30 minutes, so check containment rather than UTC alignment.
		first := hr.Add(time.Minute)
		if b.Start.After(first) || !b.Start.Add(time.Hour).After(first) {
			t.Fatalf("bucket start %v does not contain sample at %v", b.Start, first)
		}
	})
	t.Run("tenant isolation", func(t *testing.T) {
		bs, _ := s.Aggregate(ctx, tsstore.SeriesQuery{Tenant: "conf-b", DeviceID: "dev", PointID: "kw", WindowHours: 6, GroupBy: "hour"})
		if len(bs) != 1 || bs[0].Count != 1 || bs[0].Max != 1000 {
			t.Fatalf("tenant b saw %+v", bs)
		}
	})
	t.Run("empty window is not an error", func(t *testing.T) {
		bs, err := s.Aggregate(ctx, tsstore.SeriesQuery{Tenant: "conf-a", DeviceID: "dev", PointID: "none", WindowHours: 6, GroupBy: "hour"})
		if err != nil || len(bs) != 0 {
			t.Fatalf("got %v, %v", bs, err)
		}
	})
	t.Run("ordered ascending", func(t *testing.T) {
		bs, err := s.Aggregate(ctx, tsstore.SeriesQuery{Tenant: "conf-a", DeviceID: "dev", PointID: "kw", WindowHours: 6, GroupBy: "15min"})
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(bs); i++ {
			if bs[i].Start.Before(bs[i-1].Start) {
				t.Fatalf("not ordered: %v", bs)
			}
		}
	})
	t.Run("rejects unknown group_by", func(t *testing.T) {
		if _, err := s.Aggregate(ctx, tsstore.SeriesQuery{Tenant: "conf-a", DeviceID: "dev", PointID: "kw", WindowHours: 6, GroupBy: "year"}); err == nil {
			t.Fatal("accepted group_by year")
		}
	})
}
