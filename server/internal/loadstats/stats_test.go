package loadstats

import (
	"testing"
	"time"
)

func TestPercentiles(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	s := Percentiles(ds)
	if s.Count != 100 {
		t.Fatalf("count %d", s.Count)
	}
	if s.P50 < 49 || s.P50 > 52 {
		t.Fatalf("p50 %v", s.P50)
	}
	if s.P95 < 94 || s.P95 > 96 {
		t.Fatalf("p95 %v", s.P95)
	}
	if s.Max != 100 {
		t.Fatalf("max %v", s.Max)
	}
}

func TestPercentilesEmpty(t *testing.T) {
	if s := Percentiles(nil); s.Count != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestSLOCheck(t *testing.T) {
	good := Summary{Count: 10, P95: 100}
	if _, ok := SLOCheck("q", good, 500); !ok {
		t.Fatal("should pass")
	}
	bad := Summary{Count: 10, P95: 600}
	if _, ok := SLOCheck("q", bad, 500); ok {
		t.Fatal("should fail")
	}
	if _, ok := SLOCheck("q", Summary{}, 500); ok {
		t.Fatal("no samples must fail")
	}
}
