package store

import "testing"

func TestPoolSize(t *testing.T) {
	cases := []struct {
		env  string
		cpus int
		want int32
	}{{"", 2, 20}, {"", 16, 64}, {"5", 8, 20}, {"100", 2, 100}, {"junk", 2, 20}, {"-3", 2, 20}}
	for _, c := range cases {
		if got := PoolSize(c.env, c.cpus); got != c.want {
			t.Errorf("PoolSize(%q,%d)=%d want %d", c.env, c.cpus, got, c.want)
		}
	}
}
