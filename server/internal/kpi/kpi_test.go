package kpi

import (
	"errors"
	"strings"
	"testing"
)

func ev(t *testing.T, src string, vals map[string]float64) float64 {
	t.Helper()
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	v, err := e.Eval(vals)
	if err != nil {
		t.Fatalf("eval %q: %v", src, err)
	}
	return v
}

func TestPrecedenceAndRefs(t *testing.T) {
	vals := map[string]float64{"m1.kwh": 10, "m2.kwh": 5}
	if v := ev(t, "{m1.kwh} + {m2.kwh} * 2", vals); v != 20 {
		t.Fatalf("precedence: %v", v)
	}
	if v := ev(t, "({m1.kwh} + {m2.kwh}) * 2", vals); v != 30 {
		t.Fatalf("parens: %v", v)
	}
	if v := ev(t, "-{m1.kwh} / 4", vals); v != -2.5 {
		t.Fatalf("unary minus: %v", v)
	}
	e, _ := Parse("{m1.kwh} + {m1.kwh} + {m2.kwh}")
	if len(e.Refs) != 2 {
		t.Fatalf("refs must be distinct: %v", e.Refs)
	}
}

func TestEvalErrors(t *testing.T) {
	e, _ := Parse("1 / {a.b}")
	if _, err := e.Eval(map[string]float64{"a.b": 0}); !errors.Is(err, ErrDivZero) {
		t.Fatalf("want div by zero, got %v", err)
	}
	if _, err := e.Eval(map[string]float64{}); err == nil {
		t.Fatal("missing value must error")
	}
}

func TestParseRejects(t *testing.T) {
	deep := strings.Repeat("(", 50) + "1" + strings.Repeat(")", 50)
	many := ""
	for i := 0; i < MaxRefs+1; i++ {
		many += "{d" + string(rune('a'+i)) + ".p}+"
	}
	for _, bad := range []string{"", "1 +", "{a}", "{a.}", "{.b}", "{a.b", "(1", "1 2", "sin(1)", "{a b.c}", "1 ; drop", "{a.b}}", strings.Repeat("1+", 200) + "1", deep, many + "1"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
