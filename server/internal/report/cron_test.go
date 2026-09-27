package report

import (
	"testing"
	"time"
)

func TestNextRun(t *testing.T) {
	from := time.Date(2026, 9, 27, 10, 30, 0, 0, time.UTC) // Sunday
	cases := []struct {
		expr string
		want time.Time
	}{
		{"* * * * *", time.Date(2026, 9, 27, 10, 31, 0, 0, time.UTC)},
		{"0 6 * * *", time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)},
		{"*/15 * * * *", time.Date(2026, 9, 27, 10, 45, 0, 0, time.UTC)},
		{"30 10 * * *", time.Date(2026, 9, 28, 10, 30, 0, 0, time.UTC)}, // today's passed
		{"0 6 * * 1", time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)},     // Monday
		{"0 0 1 * *", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, err := NextRun(c.expr, from)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s: got %v err %v, want %v", c.expr, got, err, c.want)
		}
	}
	for _, bad := range []string{"* * * *", "61 * * * *", "1/5 * * * *", "*/0 * * * *", "a b c d e"} {
		if _, err := NextRun(bad, from); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}
