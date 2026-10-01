// Package anomaly flags outliers in a time series with a robust, explainable
// statistic (median and MAD based z-score). No model training and no external
// service, so it works air-gapped and its decisions can be explained to an
// operator: "value 97 is 6.2 robust deviations from the median 71".
package anomaly

import (
	"math"
	"sort"
)

type Sample struct {
	T     int64 // unix seconds
	Value float64
}

type Finding struct {
	T      int64   `json:"t"`
	Value  float64 `json:"value"`
	Score  float64 `json:"score"`  // robust z-score, signed
	Median float64 `json:"median"` // baseline for the whole window
}

// MinSamples is the smallest window that gives a meaningful baseline.
const MinSamples = 20

func median(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// Detect returns the samples whose robust z-score magnitude exceeds threshold
// (3.5 is the conventional cut-off, Iglewicz and Hoaglin). A constant series
// (MAD 0) uses mean absolute deviation instead; if that is also 0 nothing is
// flagged, because no deviation can be defined. Fewer than MinSamples returns
// ok=false rather than guessing.
func Detect(s []Sample, threshold float64) (findings []Finding, ok bool) {
	if len(s) < MinSamples || threshold <= 0 {
		return nil, false
	}
	vals := make([]float64, len(s))
	for i, x := range s {
		vals[i] = x.Value
	}
	med := median(vals)
	dev := make([]float64, len(vals))
	for i, v := range vals {
		dev[i] = math.Abs(v - med)
	}
	mad := median(dev)
	var scale float64
	if mad > 0 {
		scale = 1.4826 * mad // consistent with the standard deviation for normal data
	} else {
		var sum float64
		for _, d := range dev {
			sum += d
		}
		meanAD := sum / float64(len(dev))
		if meanAD == 0 {
			return nil, true
		}
		scale = 1.2533 * meanAD
	}
	for i, x := range s {
		z := (vals[i] - med) / scale
		if math.Abs(z) > threshold {
			findings = append(findings, Finding{T: x.T, Value: x.Value, Score: math.Round(z*100) / 100, Median: med})
		}
	}
	return findings, true
}
