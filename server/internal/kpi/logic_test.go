package kpi

import (
	"strings"
	"testing"
)

func TestAndOrNot(t *testing.T) {
	vals := map[string]float64{"d.a": 5, "d.b": 10, "d.z": 0}
	ok := map[string]float64{
		"if({d.a} > 1 and {d.b} > 1, 1, 0)":                       1,
		"if({d.a} > 1 and {d.b} < 1, 1, 0)":                       0,
		"if({d.a} < 1 or {d.b} > 1, 1, 0)":                        1,
		"if({d.a} < 1 or {d.b} < 1, 1, 0)":                        0,
		"if(not {d.a} < 1, 1, 0)":                                 1,
		"if(not not {d.a} < 1, 1, 0)":                             0,
		"if({d.a} < 1 or {d.a} > 1 and {d.b} > 99, 1, 0)":         0, // and binds tighter than or
		"if(({d.a} < 1 or {d.a} > 1) and {d.b} > 1, 1, 0)":        1,
		"if(not ({d.a} > 1 and {d.b} > 1), 1, 0)":                 0,
		"if(({d.a} + 1) * 2 > 11, 1, 0)":                          1, // arithmetic group still works
		"if({d.z} != 0 and 1 / {d.z} > 2, 1, 0)":                  0, // and short-circuits
		"if({d.z} == 0 or 1 / {d.z} > 2, 1, 0)":                   1, // or short-circuits
		"if({d.a} > 1 and ({d.b} > 100 or not {d.z} != 0), 7, 8)": 7,
	}
	for src, want := range ok {
		e, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		got, err := e.Eval(vals)
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v; want %v", src, got, err, want)
		}
	}
	for _, bad := range []string{
		"if({d.a} > 1 and, 1, 0)", "if(and {d.a} > 1, 1, 0)", "if({d.a} > 1 or or {d.b} > 1, 1, 0)",
		"if(not, 1, 0)", "if({d.a} > 1 && {d.b} > 1, 1, 0)", "if(({d.a} > 1, 1, 0)", "if({d.a} > 1 andx {d.b} > 1, 1, 0)",
		"if(" + strings.Repeat("not ", 40) + "1 > 0, 1, 0)",
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	// evaluated side still reports errors
	e, _ := Parse("if({d.a} > 1 and 1 / {d.z} > 2, 1, 0)")
	if _, err := e.Eval(vals); err == nil {
		t.Fatal("evaluated division by zero must error")
	}
}
