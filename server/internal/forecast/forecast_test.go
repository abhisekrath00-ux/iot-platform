package forecast

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func seasonal(n, m int, trend, noise float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	y := make([]float64, n)
	for i := range y {
		y[i] = 50 + trend*float64(i) + 10*math.Sin(2*math.Pi*float64(i%m)/float64(m)) + noise*r.NormFloat64()
	}
	return y
}

func TestForecastBeatsNaiveOnSeasonalTrend(t *testing.T) {
	y := seasonal(24*14, 24, 0.05, 0.5, 1)
	bt, err := RunBacktest(y, 24, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !bt.Useful || bt.MAE > 1.5 {
		t.Fatalf("backtest %+v", bt)
	}
	m, _ := Fit(y, 24)
	fc := m.Forecast(24)
	truth := seasonal(24*15, 24, 0.05, 0, 1)[24*14:]
	var in int
	for i, p := range fc {
		if truth[i] >= p.Lower && truth[i] <= p.Upper {
			in++
		}
	}
	if in < 20 {
		t.Fatalf("only %d/24 true values inside the interval", in)
	}
}

func TestBacktestFlagsUselessModelOnNoise(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	y := make([]float64, 24*10)
	for i := range y {
		y[i] = r.NormFloat64()
	}
	bt, err := RunBacktest(y, 24, 24)
	if err != nil {
		t.Fatal(err)
	}
	if bt.Useful && bt.MAE > 0.95*bt.NaiveMAE {
		t.Fatalf("noise model claimed useful with no real margin: %+v", bt)
	}
}

func TestFitGuards(t *testing.T) {
	if _, err := Fit(make([]float64, 10), 24); err != ErrTooShort {
		t.Fatal(err)
	}
	if _, err := Fit(make([]float64, 100), 1); err != ErrBadSeason {
		t.Fatal(err)
	}
}

func TestFirstCrossing(t *testing.T) {
	y := seasonal(24*10, 24, 0.5, 0.2, 3)
	m, _ := Fit(y, 24)
	fc := m.Forecast(48)
	if s := FirstCrossing(fc, 1e9, true); s != 0 {
		t.Fatalf("impossible limit crossed at %d", s)
	}
	if s := FirstCrossing(fc, fc[10].Value-0.001, true); s == 0 || s > 11 {
		t.Fatalf("crossing step = %d", s)
	}
}

func TestCUSUMFindsLevelShift(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	y := make([]float64, 300)
	for i := range y {
		y[i] = 10 + r.NormFloat64()*0.5
		if i >= 150 {
			y[i] += 3
		}
	}
	got := CUSUM(y, 50, 0.5, 5)
	if len(got) == 0 || got[0] < 150 || got[0] > 160 {
		t.Fatalf("change points %v, want first near 150", got)
	}
	flat := make([]float64, 300)
	for i := range flat {
		flat[i] = 10 + r.NormFloat64()*0.5
	}
	if c := CUSUM(flat, 50, 0.5, 8); len(c) != 0 {
		t.Fatalf("false change points on stationary series: %v", c)
	}
}

func TestRankRelatedFindsLeader(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	n := 200
	lead := make([]float64, n)
	for i := range lead {
		lead[i] = r.NormFloat64()
	}
	target := make([]float64, n)
	for i := 3; i < n; i++ {
		target[i] = 2*lead[i-3] + 0.1*r.NormFloat64()
	}
	noise := make([]float64, n)
	for i := range noise {
		noise[i] = r.NormFloat64()
	}
	hints := RankRelated(target, map[string][]float64{"pump-pressure": lead, "ambient": noise, "short": {1, 2}}, 6, 0.5)
	if len(hints) != 1 || hints[0].Name != "pump-pressure" || hints[0].Lag != 3 || hints[0].R < 0.95 {
		t.Fatalf("hints %+v", hints)
	}
}

func TestCUSUMOnSeasonalNeedsDeseasonalizing(t *testing.T) {
	y := seasonal(24*14, 24, 0, 0.3, 4)
	if c := CUSUM(Deseasonalize(y, 24), 48, 0.5, 8); len(c) != 0 {
		t.Fatalf("false change points on a pure daily cycle: %v", c)
	}
	for i := 24 * 8; i < len(y); i++ {
		y[i] += 6
	}
	c := CUSUM(Deseasonalize(y, 24), 48, 0.5, 8)
	if len(c) == 0 {
		t.Fatal("level shift missed")
	}
}

func TestUsefulMargin(t *testing.T) {
	if usefulMargin(0, 0) != true || usefulMargin(1, 1) || !usefulMargin(0.9, 1) {
		t.Fatal("margin rule wrong")
	}
}

func TestReadingWording(t *testing.T) {
	a := Reading("d1", "pressure", 0.82, 2)
	if !strings.Contains(a, "earlier") || !strings.Contains(a, "together") || strings.Contains(strings.ToLower(a), "caus") {
		t.Errorf("%q", a)
	}
	if b := Reading("d1", "flow", -0.7, -3); !strings.Contains(b, "opposite") || !strings.Contains(b, "about 3 h later") {
		t.Errorf("%q", b)
	}
	if c := Reading("d1", "x", 0.9, 0); !strings.Contains(c, "at the same time") {
		t.Errorf("%q", c)
	}
}
