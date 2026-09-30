package health

import (
	"testing"
	"time"
)

func TestScore(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	cases := []struct {
		name   string
		in     Input
		status string
		min    int
		max    int
	}{
		{"fresh and clean", Input{LastSeen: ago(10 * time.Second), Now: now, Samples: 50, MeasuredSamples: 50, GatewayOnline: true}, "healthy", 100, 100},
		{"never seen", Input{Now: now, GatewayOnline: true}, "offline", 0, 20},
		{"stale 10 min at 60s interval", Input{LastSeen: ago(10 * time.Minute), Now: now, Samples: 50, MeasuredSamples: 50, GatewayOnline: true}, "degraded", 50, 90},
		{"dead for hours", Input{LastSeen: ago(6 * time.Hour), Now: now, Samples: 50, MeasuredSamples: 50, GatewayOnline: false}, "offline", 0, 50},
		{"poor quality", Input{LastSeen: ago(5 * time.Second), Now: now, Samples: 10, MeasuredSamples: 2, GatewayOnline: true}, "degraded", 50, 90},
		{"custom interval respected", Input{LastSeen: ago(10 * time.Minute), Now: now, Interval: 5 * time.Minute, Samples: 5, MeasuredSamples: 5, GatewayOnline: true}, "healthy", 100, 100},
	}
	for _, c := range cases {
		r := Score(c.in)
		if r.Status != c.status || r.Score < c.min || r.Score > c.max {
			t.Errorf("%s: got %d %s, want %s in [%d,%d]", c.name, r.Score, r.Status, c.status, c.min, c.max)
		}
		if len(r.Factors) != 3 {
			t.Errorf("%s: factors = %d", c.name, len(r.Factors))
		}
	}
}
