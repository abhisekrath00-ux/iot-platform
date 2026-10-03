package kpi

import (
	"math"
	"testing"
)

// The OEE template the UI builds must parse and evaluate in the real KPI language.
func TestOEETemplateExpression(t *testing.T) {
	e, err := Parse("({l1.run} / {l1.planned}) * ({l1.total} * 0.5 / {l1.run}) * ({l1.good} / {l1.total})")
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Eval(map[string]float64{"l1.run": 400, "l1.planned": 480, "l1.total": 700, "l1.good": 665})
	if err != nil || math.Abs(v-0.69271) > 1e-4 {
		t.Fatalf("%v %v", v, err)
	}
	if _, err := e.Eval(map[string]float64{"l1.run": 0, "l1.planned": 480, "l1.total": 700, "l1.good": 665}); err == nil {
		t.Fatal("zero run time must be an error, not a number")
	}
}
