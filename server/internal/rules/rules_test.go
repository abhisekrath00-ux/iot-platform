package rules

import "testing"

func TestThresholdLogic(t *testing.T) {
	cases := []struct {
		op        string
		threshold float64
		value     float64
		want      bool
	}{
		{">", 100, 101, true},
		{">", 100, 100, false},
		{">", 100, 99, false},
		{"<", 0, -1, true},
		{"<", 0, 0, false},
		{"<", 0, 1, false},
	}
	for _, c := range cases {
		hit := (c.op == ">" && c.value > c.threshold) || (c.op == "<" && c.value < c.threshold)
		if hit != c.want {
			t.Errorf("op=%s threshold=%v value=%v: got %v want %v", c.op, c.threshold, c.value, hit, c.want)
		}
	}
}
