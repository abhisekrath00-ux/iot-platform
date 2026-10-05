package main

import (
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/aitools"
)

func TestCasesAreWellFormed(t *testing.T) {
	cs := cases()
	if len(cs) < 100 {
		t.Fatalf("only %d tool-selection cases", len(cs))
	}
	covered := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c.Q] {
			t.Errorf("duplicate question %q", c.Q)
		}
		seen[c.Q] = true
		for _, w := range c.Want {
			if _, ok := aitools.Lookup(w); !ok {
				t.Errorf("%q expects unknown tool %s", c.Q, w)
			}
			covered[w] = true
		}
	}
	for _, n := range aitools.Names() {
		if !covered[n] {
			t.Errorf("tool %s has no case", n)
		}
	}
}
