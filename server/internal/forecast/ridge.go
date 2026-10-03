package forecast

import (
	"errors"
	"math"
)

// Learned model: a ridge-regularised autoregression fitted by least squares on
// the series itself. Features for hour t: the previous three hours, the same
// hour yesterday, and the hour of day as a sine/cosine pair, plus an intercept.
// It is "learned" in the sense that coefficients are fitted from data, nothing
// more: no neural network, no pre-trained weights, no data from other devices.
// It is fitted on demand per request and not stored.

const (
	ridgeLambda = 1.0
	ridgeLags   = 24
)

var ErrRidgeShort = errors.New("need at least 4 days of hourly data for the learned model")

// RidgeModel holds fitted coefficients and the history needed to roll forward.
type RidgeModel struct {
	W     []float64
	Sigma float64
	hist  []float64
	phase int // hour of day of hist[0]
}

func ridgeFeatures(y []float64, i, phase int) []float64 {
	h := 2 * math.Pi * float64((i+phase)%24) / 24
	return []float64{1, y[i-1], y[i-2], y[i-3], y[i-24], math.Sin(h), math.Cos(h)}
}

// FitRidge fits on y, where y[0] falls at hour-of-day phase.
func FitRidge(y []float64, phase int) (*RidgeModel, error) {
	if len(y) < 4*24 {
		return nil, ErrRidgeShort
	}
	var mean float64
	for _, v := range y {
		mean += v
	}
	mean /= float64(len(y))
	yc := make([]float64, len(y))
	for i, v := range y {
		yc[i] = v - mean
	}
	const d = 7
	var A [d][d + 1]float64
	for i := ridgeLags; i < len(yc); i++ {
		x := ridgeFeatures(yc, i, phase)
		for r := 0; r < d; r++ {
			for c := 0; c < d; c++ {
				A[r][c] += x[r] * x[c]
			}
			A[r][d] += x[r] * yc[i]
		}
	}
	for r := 1; r < d; r++ { // do not penalise the intercept
		A[r][r] += ridgeLambda
	}
	w, ok := solve(A)
	if !ok {
		return nil, errors.New("singular system")
	}
	var sse float64
	n := 0
	for i := ridgeLags; i < len(yc); i++ {
		x := ridgeFeatures(yc, i, phase)
		var p float64
		for k := range w {
			p += w[k] * x[k]
		}
		sse += (yc[i] - p) * (yc[i] - p)
		n++
	}
	// Coefficients were fitted on the centred series; fold the mean back into the intercept path
	// by keeping hist centred and re-adding the mean in Forecast.
	m := &RidgeModel{W: append(w[:0:0], w...), Sigma: math.Sqrt(sse / float64(n)), hist: yc, phase: phase}
	m.W = append(m.W, mean) // last element: mean, not a coefficient
	return m, nil
}

// Forecast rolls the model forward h steps, feeding predictions back as lags.
// Intervals are an approximation: 1.96*sigma*sqrt(step), residual sigma from training.
func (m *RidgeModel) Forecast(h int) []Point {
	mean := m.W[len(m.W)-1]
	w := m.W[:len(m.W)-1]
	y := append([]float64(nil), m.hist...)
	out := make([]Point, 0, h)
	for k := 1; k <= h; k++ {
		y = append(y, 0)
		x := ridgeFeatures(y, len(y)-1, m.phase)
		var p float64
		for j := range w {
			p += w[j] * x[j]
		}
		y[len(y)-1] = p
		v := p + mean
		band := 1.96 * m.Sigma * math.Sqrt(float64(k))
		out = append(out, Point{Step: k, Value: v, Lower: v - band, Upper: v + band})
	}
	return out
}

// RunRidgeBacktest holds out the last `holdout` hours, fits on the rest and
// compares against the seasonal-naive baseline (repeat the last day).
func RunRidgeBacktest(y []float64, phase, holdout int) (Backtest, error) {
	if holdout < 1 || len(y)-holdout < 4*24 {
		return Backtest{}, ErrRidgeShort
	}
	train, test := y[:len(y)-holdout], y[len(y)-holdout:]
	mod, err := FitRidge(train, phase)
	if err != nil {
		return Backtest{}, err
	}
	var mae, nmae float64
	for i, p := range mod.Forecast(holdout) {
		mae += math.Abs(test[i] - p.Value)
		nmae += math.Abs(test[i] - train[len(train)-24+(i%24)])
	}
	mae /= float64(holdout)
	nmae /= float64(holdout)
	return Backtest{MAE: mae, NaiveMAE: nmae, Useful: usefulMargin(mae, nmae), Holdout: holdout}, nil
}

// solve does Gaussian elimination with partial pivoting on an augmented d x (d+1) matrix.
func solve(A [7][8]float64) ([]float64, bool) {
	const d = 7
	for c := 0; c < d; c++ {
		p := c
		for r := c + 1; r < d; r++ {
			if math.Abs(A[r][c]) > math.Abs(A[p][c]) {
				p = r
			}
		}
		if math.Abs(A[p][c]) < 1e-12 {
			return nil, false
		}
		A[c], A[p] = A[p], A[c]
		for r := c + 1; r < d; r++ {
			f := A[r][c] / A[c][c]
			for k := c; k <= d; k++ {
				A[r][k] -= f * A[c][k]
			}
		}
	}
	w := make([]float64, d)
	for r := d - 1; r >= 0; r-- {
		s := A[r][d]
		for k := r + 1; k < d; k++ {
			s -= A[r][k] * w[k]
		}
		w[r] = s / A[r][r]
	}
	return w, true
}
