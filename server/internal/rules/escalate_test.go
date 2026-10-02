package rules

import "testing"

func TestNextStepOrderAndSeverity(t *testing.T) {
	steps := []Step{
		{Severity: "", Step: 1, AfterMinutes: 10, ChannelID: "any1"},
		{Severity: "", Step: 2, AfterMinutes: 30, ChannelID: "any2"},
		{Severity: "critical", Step: 1, AfterMinutes: 5, ChannelID: "crit1"},
	}
	if _, ok := NextStep(steps, "warning", 0, 9); ok {
		t.Error("not due yet")
	}
	if s, ok := NextStep(steps, "warning", 0, 12); !ok || s.ChannelID != "any1" {
		t.Errorf("warning step1 = %+v %v", s, ok)
	}
	if s, ok := NextStep(steps, "warning", 1, 500); !ok || s.ChannelID != "any2" {
		t.Errorf("one step at a time, in order: %+v", s)
	}
	if _, ok := NextStep(steps, "warning", 2, 5000); ok {
		t.Error("no steps left")
	}
	if s, ok := NextStep(steps, "critical", 0, 6); !ok || s.ChannelID != "crit1" {
		t.Errorf("critical uses its own steps: %+v", s)
	}
	if _, ok := NextStep(steps, "critical", 1, 5000); ok {
		t.Error("critical has one own step; generic steps must not also apply")
	}
	if _, ok := NextStep(nil, "info", 0, 99); ok {
		t.Error("no policy, no escalation")
	}
}

func TestValidateSteps(t *testing.T) {
	good := []Step{{Step: 1, AfterMinutes: 5, ChannelID: "c"}, {Step: 2, AfterMinutes: 15, ChannelID: "d"}}
	if err := ValidateSteps(good); err != nil {
		t.Fatal(err)
	}
	bad := map[string][]Step{
		"gap":         {{Step: 2, AfterMinutes: 5, ChannelID: "c"}},
		"not growing": {{Step: 1, AfterMinutes: 5, ChannelID: "c"}, {Step: 2, AfterMinutes: 5, ChannelID: "c"}},
		"severity":    {{Severity: "bogus", Step: 1, AfterMinutes: 5, ChannelID: "c"}},
		"no channel":  {{Step: 1, AfterMinutes: 5}},
		"zero min":    {{Step: 1, AfterMinutes: 0, ChannelID: "c"}},
	}
	for name, s := range bad {
		if ValidateSteps(s) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
