// Package forecast holds the statistical (not learned) analytics: additive
// Holt-Winters forecasting with prediction intervals and a backtest, CUSUM
// change-point detection, and lagged-correlation ranking for root-cause hints.
// Everything is deterministic, needs no training data beyond the series
// itself, and runs air-gapped. Outputs say "correlated with", never "caused by".
package forecast

import (
	"errors"
	"math"
	"sort"
)

var (
	ErrTooShort  = errors.New("series too short: need at least 3 full seasons")
	ErrBadSeason = errors.New("season length must be >= 2")
)

// Model is a fitted additive Holt-Winters model (level, trend, seasonal).
type Model struct {
	M                  int
	Alpha, Beta, Gamma float64
	level, trend       float64
	season             []float64
	Sigma              float64 // std of one-step in-sample residuals
	n                  int
}

type state struct {
	l, b float64
	s    []float64
}

func initState(y []float64, m int) state {
	var m1, m2 float64
	for i := 0; i < m; i++ {
		m1 += y[i]
		m2 += y[m+i]
	}
	m1 /= float64(m)
	m2 /= float64(m)
	st := state{l: m1, b: (m2 - m1) / float64(m), s: make([]float64, m)}
	for i := 0; i < m; i++ {
		st.s[i] = y[i] - m1
	}
	return st
}

func run(y []float64, m int, a, b, g float64) (st state, sse float64, resid []float64) {
	st = initState(y, m)
	st.s = append([]float64(nil), st.s...)
	for t := m; t < len(y); t++ {
		si := st.s[t%m]
		pred := st.l + st.b + si
		e := y[t] - pred
		resid = append(resid, e)
		sse += e * e
		lNew := a*(y[t]-si) + (1-a)*(st.l+st.b)
		st.b = b*(lNew-st.l) + (1-b)*st.b
		st.s[t%m] = g*(y[t]-lNew) + (1-g)*si
		st.l = lNew
	}
	return
}

// Fit picks smoothing parameters from a small grid by minimising one-step SSE.
func Fit(y []float64, m int) (*Model, error) {
	if m < 2 {
		return nil, ErrBadSeason
	}
	if len(y) < 3*m {
		return nil, ErrTooShort
	}
	grid := []float64{0.05, 0.1, 0.2, 0.4, 0.6}
	bestSSE := math.Inf(1)
	var best Model
	var bestSt state
	var bestRes []float64
	for _, a := range grid {
		for _, b := range []float64{0.01, 0.05, 0.1} {
			for _, g := range grid {
				st, sse, res := run(y, m, a, b, g)
				if sse < bestSSE {
					bestSSE, best, bestSt, bestRes = sse, Model{M: m, Alpha: a, Beta: b, Gamma: g}, st, res
				}
			}
		}
	}
	best.level, best.trend, best.season, best.n = bestSt.l, bestSt.b, bestSt.s, len(y)
	var ss float64
	for _, e := range bestRes {
		ss += e * e
	}
	best.Sigma = math.Sqrt(ss / float64(len(bestRes)))
	return &best, nil
}

// Point is one forecast step with an approximate 95% interval.
type Point struct {
	Step  int     `json:"step"`
	Value float64 `json:"value"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

// Forecast returns h steps ahead. The interval widens with the horizon using
// the ETS(A,N,A)-style approximation sigma*sqrt(1+(h-1)*alpha^2); it is an
// approximation, and the backtest is the better guide to real accuracy.
func (m *Model) Forecast(h int) []Point {
	out := make([]Point, 0, h)
	for k := 1; k <= h; k++ {
		v := m.level + float64(k)*m.trend + m.season[(m.n+k-1)%m.M]
		w := 1.96 * m.Sigma * math.Sqrt(1+float64(k-1)*m.Alpha*m.Alpha)
		out = append(out, Point{Step: k, Value: v, Lower: v - w, Upper: v + w})
	}
	return out
}

// Backtest holds out the last `holdout` points, fits on the rest, forecasts
// them, and compares to a seasonal-naive forecast (repeat the last season).
// Useful reports "the model beat the naive baseline"; if false the forecast
// should not be shown as a prediction.
type Backtest struct {
	MAE, NaiveMAE float64
	Useful        bool
	Holdout       int
}

func RunBacktest(y []float64, m, holdout int) (Backtest, error) {
	if holdout < 1 || len(y)-holdout < 3*m {
		return Backtest{}, ErrTooShort
	}
	train, test := y[:len(y)-holdout], y[len(y)-holdout:]
	mod, err := Fit(train, m)
	if err != nil {
		return Backtest{}, err
	}
	fc := mod.Forecast(holdout)
	var mae, nmae float64
	for i, p := range fc {
		mae += math.Abs(test[i] - p.Value)
		nmae += math.Abs(test[i] - train[len(train)-m+(i%m)])
	}
	mae /= float64(holdout)
	nmae /= float64(holdout)
	return Backtest{MAE: mae, NaiveMAE: nmae, Useful: usefulMargin(mae, nmae), Holdout: holdout}, nil
}

// FirstCrossing returns the first forecast step whose point value is on the
// far side of limit (above when up is true), or 0 if none within the horizon.
func FirstCrossing(fc []Point, limit float64, up bool) int {
	for _, p := range fc {
		if (up && p.Value >= limit) || (!up && p.Value <= limit) {
			return p.Step
		}
	}
	return 0
}

// CUSUM finds level shifts. The baseline mean and std come from the first
// `base` points; k is the allowance and h the decision limit, both in std units
// (k=0.5, h=5 are the usual defaults). It returns the indexes where a shift is
// flagged; after each detection the baseline restarts from that point.
func CUSUM(y []float64, base int, k, h float64) []int {
	if base < 5 || len(y) <= base {
		return nil
	}
	var out []int
	start := 0
	for start+base < len(y) {
		var mean float64
		for _, v := range y[start : start+base] {
			mean += v
		}
		mean /= float64(base)
		var ss float64
		for _, v := range y[start : start+base] {
			ss += (v - mean) * (v - mean)
		}
		std := math.Sqrt(ss / float64(base))
		if std == 0 {
			std = 1e-9
		}
		var hi, lo float64
		hit := -1
		for i := start + base; i < len(y); i++ {
			z := (y[i] - mean) / std
			hi = math.Max(0, hi+z-k)
			lo = math.Max(0, lo-z-k)
			if hi > h || lo > h {
				hit = i
				break
			}
		}
		if hit < 0 {
			break
		}
		out = append(out, hit)
		start = hit
	}
	return out
}

// Hint is one candidate related signal.
type Hint struct {
	Name string  `json:"name"`
	R    float64 `json:"r"`   // Pearson r at the best lag
	Lag  int     `json:"lag"` // >0: this signal moved first by Lag steps
}

func pearson(a, b []float64) float64 {
	n := len(a)
	if n < 3 || n != len(b) {
		return 0
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	if saa == 0 || sbb == 0 {
		return 0
	}
	return sab / math.Sqrt(saa*sbb)
}

// RankRelated ranks other series by |r| with the target at the best lag in
// [-maxLag, maxLag]. Series must be aligned on the same grid. Results below
// minAbsR are dropped. This is correlation, not causation.
func RankRelated(target []float64, others map[string][]float64, maxLag int, minAbsR float64) []Hint {
	var out []Hint
	for name, o := range others {
		if len(o) != len(target) {
			continue
		}
		best := Hint{Name: name}
		for lag := -maxLag; lag <= maxLag; lag++ {
			var a, b []float64
			if lag >= 0 { // o leads target by lag
				a, b = target[lag:], o[:len(o)-lag]
			} else {
				a, b = target[:len(target)+lag], o[-lag:]
			}
			r := pearson(a, b)
			if math.Abs(r) > math.Abs(best.R) {
				best.R, best.Lag = r, lag
			}
		}
		if math.Abs(best.R) >= minAbsR {
			out = append(out, best)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if math.Abs(out[i].R) != math.Abs(out[j].R) {
			return math.Abs(out[i].R) > math.Abs(out[j].R)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// usefulMargin: the model must beat the seasonal-naive forecast by at least 5%
// (or both be effectively exact), otherwise it is not worth showing as a prediction.
func usefulMargin(mae, naive float64) bool {
	if naive < 1e-9 {
		return mae < 1e-9
	}
	return mae <= 0.95*naive
}

// Deseasonalize subtracts the mean of each position in the season (hour of
// day for m=24) so that CUSUM sees level shifts, not the daily cycle.
func Deseasonalize(y []float64, m int) []float64 {
	sum := make([]float64, m)
	cnt := make([]float64, m)
	var total float64
	for i, v := range y {
		sum[i%m] += v
		cnt[i%m]++
		total += v
	}
	grand := total / float64(len(y))
	out := make([]float64, len(y))
	for i, v := range y {
		out[i] = v - sum[i%m]/cnt[i%m] + grand
	}
	return out
}
