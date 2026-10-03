package forecast

import (
	"math"
	"math/rand"
	"testing"
)

func daily(n int, noise float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	y := make([]float64, n)
	for i := range y {
		y[i] = 50 + 10*math.Sin(2*math.Pi*float64(i%24)/24) + 0.02*float64(i) + noise*r.NormFloat64()
	}
	return y
}

func TestRidgeLearnsDailyCycleWithTrend(t *testing.T) {
	y := daily(10*24, 0.3, 1)
	m, err := FitRidge(y[:9*24], 0)
	if err != nil {
		t.Fatal(err)
	}
	fc := m.Forecast(24)
	var mae float64
	for i, p := range fc {
		mae += math.Abs(p.Value - y[9*24+i])
	}
	mae /= 24
	if mae > 1.5 {
		t.Fatalf("MAE %.2f too high for a clean daily cycle", mae)
	}
	bt, err := RunRidgeBacktest(y, 0, 24)
	if err != nil || bt.MAE >= bt.NaiveMAE {
		t.Fatalf("backtest %+v err=%v: expected to beat repeating yesterday on a trending series", bt, err)
	}
}

func TestRidgeNoiseIsNotUseful(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	y := make([]float64, 8*24)
	for i := range y {
		y[i] = 20 + r.NormFloat64()
	}
	bt, err := RunRidgeBacktest(y, 0, 24)
	if err != nil {
		t.Fatal(err)
	}
	if bt.MAE > 1.3*bt.NaiveMAE {
		t.Fatalf("pure noise should not be much worse than naive: %+v", bt)
	}
}

func TestRidgeRefusesShortAndConstant(t *testing.T) {
	if _, err := FitRidge(make([]float64, 50), 0); err == nil {
		t.Fatal("short series accepted")
	}
	c := make([]float64, 6*24)
	for i := range c {
		c[i] = 7
	}
	m, err := FitRidge(c, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Forecast(5) {
		if math.Abs(p.Value-7) > 1e-6 {
			t.Fatalf("constant series forecast %v", p.Value)
		}
	}
}

func TestRidgePhase(t *testing.T) {
	y := daily(8*24, 0.1, 3)
	shifted := y[5:] // starts at hour 5
	m, err := FitRidge(shifted[:7*24], 5)
	if err != nil {
		t.Fatal(err)
	}
	p := m.Forecast(1)[0].Value
	if math.Abs(p-shifted[7*24]) > 2 {
		t.Fatalf("phase handling wrong: forecast %.2f vs %.2f", p, shifted[7*24])
	}
}
