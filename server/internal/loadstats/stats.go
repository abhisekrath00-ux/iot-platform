// Package loadstats computes latency summaries for the load-test harness.
package loadstats

import (
	"fmt"
	"sort"
	"time"
)

// Summary holds percentile latencies in milliseconds.
type Summary struct {
	Count int
	P50   float64
	P95   float64
	P99   float64
	Max   float64
}

// Percentiles summarizes durations. Input need not be sorted.
func Percentiles(ds []time.Duration) Summary {
	if len(ds) == 0 {
		return Summary{}
	}
	ms := make([]float64, len(ds))
	for i, d := range ds {
		ms[i] = float64(d) / float64(time.Millisecond)
	}
	sort.Float64s(ms)
	pick := func(q float64) float64 {
		idx := int(q*float64(len(ms)-1) + 0.5)
		if idx >= len(ms) {
			idx = len(ms) - 1
		}
		return ms[idx]
	}
	return Summary{Count: len(ms), P50: pick(0.50), P95: pick(0.95), P99: pick(0.99), Max: ms[len(ms)-1]}
}

// SLOCheck compares a p95 against a budget and reports pass/fail.
func SLOCheck(name string, s Summary, budgetMs float64) (line string, ok bool) {
	if s.Count == 0 {
		return fmt.Sprintf("%s: NO SAMPLES", name), false
	}
	status := "PASS"
	if s.P95 > budgetMs {
		status = "FAIL"
	}
	return fmt.Sprintf("%s: p50=%.0fms p95=%.0fms p99=%.0fms max=%.0fms (slo p95<=%.0fms, n=%d) %s",
		name, s.P50, s.P95, s.P99, s.Max, budgetMs, s.Count, status), s.P95 <= budgetMs
}
