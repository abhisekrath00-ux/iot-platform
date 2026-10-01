package anomaly

import "testing"

func series(vals ...float64) []Sample {
	out := make([]Sample, len(vals))
	for i, v := range vals {
		out[i] = Sample{T: int64(i), Value: v}
	}
	return out
}

func steady(n int, base float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = base + float64(i%5)*0.2
	}
	return v
}

func TestDetectFlagsSpikeAndDip(t *testing.T) {
	v := steady(40, 70)
	v[10], v[30] = 120, 20
	f, ok := Detect(series(v...), 3.5)
	if !ok || len(f) != 2 || f[0].Value != 120 || f[0].Score <= 0 || f[1].Value != 20 || f[1].Score >= 0 {
		t.Fatalf("got %v ok=%v", f, ok)
	}
}

func TestDetectQuietSeriesHasNoFindings(t *testing.T) {
	if f, ok := Detect(series(steady(60, 50)...), 3.5); !ok || len(f) != 0 {
		t.Fatalf("got %v ok=%v", f, ok)
	}
}

func TestDetectConstantSeries(t *testing.T) {
	v := make([]float64, 30)
	for i := range v {
		v[i] = 5
	}
	if f, ok := Detect(series(v...), 3.5); !ok || len(f) != 0 {
		t.Fatalf("constant: %v %v", f, ok)
	}
	v[3] = 9 // one outlier in an otherwise constant series is still found
	if f, _ := Detect(series(v...), 3.5); len(f) != 1 || f[0].Value != 9 {
		t.Fatalf("outlier in constant series: %v", f)
	}
}

func TestDetectNeedsEnoughData(t *testing.T) {
	if _, ok := Detect(series(steady(MinSamples-1, 1)...), 3.5); ok {
		t.Fatal("must refuse a window below MinSamples")
	}
	if _, ok := Detect(series(steady(30, 1)...), 0); ok {
		t.Fatal("non-positive threshold must be refused")
	}
}

func TestMedianEvenAndOdd(t *testing.T) {
	if median([]float64{3, 1, 2}) != 2 || median([]float64{4, 1, 2, 3}) != 2.5 {
		t.Fatal("median wrong")
	}
}
