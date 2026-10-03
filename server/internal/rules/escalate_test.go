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

func TestRepeatDue(t *testing.T) {
	steps := []Step{
		{Step: 1, AfterMinutes: 10, ChannelID: "a"},
		{Step: 2, AfterMinutes: 30, ChannelID: "b"},
		{Severity: "critical", Step: 1, AfterMinutes: 5, ChannelID: "c"},
	}
	r := Repeat{EveryMinutes: 15, Max: 2}
	if _, ok := RepeatDue(steps, "warning", 1, 0, 99, r); ok {
		t.Error("chain not finished: step 2 still to send")
	}
	if s, ok := RepeatDue(steps, "warning", 2, 0, 15, r); !ok || s.ChannelID != "b" {
		t.Errorf("last step resent: %+v %v", s, ok)
	}
	if _, ok := RepeatDue(steps, "warning", 2, 0, 14, r); ok {
		t.Error("too soon")
	}
	if _, ok := RepeatDue(steps, "warning", 2, 2, 999, r); ok {
		t.Error("max resends reached")
	}
	if s, ok := RepeatDue(steps, "critical", 1, 1, 20, r); !ok || s.ChannelID != "c" {
		t.Errorf("critical repeats its own last step: %+v", s)
	}
	if _, ok := RepeatDue(steps, "warning", 2, 0, 999, Repeat{}); ok {
		t.Error("off by default")
	}
	if _, ok := RepeatDue(steps, "warning", 0, 0, 999, r); ok {
		t.Error("never before the first step")
	}
}

func TestValidateRepeat(t *testing.T) {
	for _, r := range []Repeat{{}, {EveryMinutes: 5, Max: 1}, {EveryMinutes: 1440, Max: 10}} {
		if err := ValidateRepeat(r); err != nil {
			t.Errorf("%+v rejected: %v", r, err)
		}
	}
	for _, r := range []Repeat{{EveryMinutes: 4, Max: 1}, {EveryMinutes: 1441, Max: 1}, {EveryMinutes: 10}, {EveryMinutes: 10, Max: 11}, {Max: 3}} {
		if ValidateRepeat(r) == nil {
			t.Errorf("%+v accepted", r)
		}
	}
}
