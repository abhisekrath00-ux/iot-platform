package driver

import "testing"

func TestParseLine(t *testing.T) {
	cases := []struct {
		in   string
		key  string
		want float64
	}{
		{`{"temp":23.4,"door":true}`, "door", 1},
		{`{"temp":23.4}`, "temp", 23.4},
		{"temp=23.4,hum=51", "hum", 51},
		{"T=1.5 H=2", "H", 2},
		{"23.4,51,1013", "2", 1013},
	}
	for _, c := range cases {
		got := parseLine(c.in)
		if got[c.key] != c.want {
			t.Errorf("%q: key %s = %v, want %v (%v)", c.in, c.key, got[c.key], c.want, got)
		}
	}
	if len(parseLine("garbage")) != 0 || len(parseLine("")) != 0 {
		t.Error("garbage should yield nothing")
	}
}
