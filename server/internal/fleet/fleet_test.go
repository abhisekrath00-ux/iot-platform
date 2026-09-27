package fleet

import (
	"fmt"
	"testing"
)

func TestValidateStages(t *testing.T) {
	for _, bad := range [][]int{{}, {0, 100}, {50, 40, 100}, {10, 50}, {101}, {110}} {
		if err := ValidateStages(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	for _, good := range [][]int{{100}, {10, 50, 100}, {5, 25, 75, 100}} {
		if err := ValidateStages(good); err != nil {
			t.Fatalf("%v rejected: %v", good, err)
		}
	}
}

func TestCohortDeterministic(t *testing.T) {
	if Cohort("c1", "gw-7", 50) != Cohort("c1", "gw-7", 50) {
		t.Fatal("not deterministic")
	}
	// different campaigns bucket independently
	_ = Cohort("c2", "gw-7", 50)
}

func TestCohortPercentageApproximate(t *testing.T) {
	serials := make([]string, 1000)
	for i := range serials {
		serials[i] = fmt.Sprintf("gw-%d", i)
	}
	in := CohortForStage("c1", serials, []int{10, 50, 100}, 0)
	if len(in) < 60 || len(in) > 140 { // ~10% of 1000
		t.Fatalf("stage0 cohort %d", len(in))
	}
	rest := CohortForStage("c1", serials, []int{10, 50, 100}, 1)
	if len(rest) < 320 || len(rest) > 480 { // ~40% of 1000
		t.Fatalf("stage1 cohort %d", len(rest))
	}
	last := CohortForStage("c1", serials, []int{10, 50, 100}, 2)
	if len(in)+len(rest)+len(last) != 1000 {
		t.Fatalf("stages do not partition: %d+%d+%d", len(in), len(rest), len(last))
	}
}

func TestTransition(t *testing.T) {
	for _, ok := range [][2]string{{Draft, Running}, {Running, Paused}, {Paused, Running}, {Running, Done}, {Running, Aborted}, {Draft, Aborted}} {
		if err := Transition(ok[0], ok[1]); err != nil {
			t.Fatalf("%s->%s: %v", ok[0], ok[1], err)
		}
	}
	for _, bad := range [][2]string{{Draft, Done}, {Done, Running}, {Aborted, Running}, {Draft, Paused}} {
		if err := Transition(bad[0], bad[1]); err == nil {
			t.Fatalf("%s->%s accepted", bad[0], bad[1])
		}
	}
}

func TestHalted(t *testing.T) {
	if !Halted(3, 3) || Halted(2, 3) || Halted(10, 0) {
		t.Fatal("halt logic wrong")
	}
}
