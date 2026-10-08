package kpi

import "testing"

func TestFunctions(t *testing.T) {
	vals := map[string]float64{"d.a": -4, "d.b": 9}
	ok := map[string]float64{
		"abs({d.a})": 4, "sqrt({d.b})": 3, "round(2.5)": 3, "min({d.a},{d.b})": -4, "max({d.a}, {d.b})": 9,
		"clamp({d.b}, 0, 5)": 5, "clamp(-1,0,5)": 0, "abs({d.a}) * 2 + max(1,2)": 10, "sqrt(abs({d.a}))": 2, "-abs(3)": -3,
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
	e, _ := Parse("abs({d.a}) + max({d.b},{x.y})")
	if len(e.Refs) != 3 {
		t.Fatalf("refs inside calls must be collected: %v", e.Refs)
	}
	for _, bad := range []string{"abs()", "abs(1,2)", "min(1)", "clamp(1,2)", "foo(1)", "abs", "abs(1", "abs 1", "exec(1)", "min(1,)", "ABS(1)", "abs((((((((((((((((((((((((((((((((((1))))))))))))))))))))))))))))))))))"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, src := range []string{"sqrt({d.a})", "clamp(1,5,0)"} {
		e, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Eval(vals); err == nil {
			t.Fatalf("%s must error", src)
		}
	}
}

func TestIf(t *testing.T) {
	vals := map[string]float64{"d.a": 5, "d.z": 0}
	ok := map[string]float64{
		"if({d.a} > 3, 1, 2)": 1, "if({d.a} < 3, 1, 2)": 2, "if({d.a} >= 5, 10, 20)": 10, "if({d.a} <= 4, 10, 20)": 20,
		"if({d.a} == 5, 1, 0)": 1, "if({d.a} != 5, 1, 0)": 0, "if({d.z} == 0, 0, 1 / {d.z})": 0,
		"if(1 + 1 > 1, max(1,2), 0) * 3": 6, "if({d.a} > 1, if({d.a} > 4, 7, 8), 9)": 7,
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
	for _, bad := range []string{"if(1, 2, 3)", "if(1 > 2, 3)", "if(1 > 2, 3, 4, 5)", "if 1 > 2, 3, 4", "if(1 > 2 > 3, 1, 2)", "if(1 = 2, 3, 4)", "if(1 > 2, 3, 4"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if e, _ := Parse("if({d.a} > 1, 1 / 0, 2)"); e != nil {
		if _, err := e.Eval(vals); err == nil {
			t.Fatal("taken branch must still report division by zero")
		}
	}
}
