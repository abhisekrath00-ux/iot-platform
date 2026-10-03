package rules

import (
	"testing"
	"time"
)

func TestOnCallRotation(t *testing.T) {
	a := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := Schedule{Anchor: a, ShiftHours: 12, ChannelIDs: []string{"A", "B", "C"}}
	cases := []struct {
		at   time.Time
		want string
	}{
		{a, "A"},
		{a.Add(11*time.Hour + 59*time.Minute), "A"},
		{a.Add(12 * time.Hour), "B"},
		{a.Add(24 * time.Hour), "C"},
		{a.Add(36 * time.Hour), "A"},   // wraps
		{a.Add(-1 * time.Minute), "C"}, // before the anchor the rotation runs backwards
		{a.Add(-12 * time.Hour), "C"},
		{a.Add(-12*time.Hour - time.Minute), "B"},
	}
	for _, c := range cases {
		got, ends, ok := OnCall(s, c.at)
		if !ok || got != c.want {
			t.Errorf("at %v: got %q want %q", c.at, got, c.want)
		}
		if ok && (!ends.After(c.at) || ends.Sub(c.at) > 12*time.Hour) {
			t.Errorf("at %v: shift end %v is not within one shift", c.at, ends)
		}
	}
	if _, _, ok := OnCall(Schedule{Anchor: a, ShiftHours: 0, ChannelIDs: []string{"A"}}, a); ok {
		t.Error("zero shift length must not resolve")
	}
}

func TestValidateScheduleAndSteps(t *testing.T) {
	good := Schedule{Name: "Plant", Anchor: time.Now(), ShiftHours: 24, ChannelIDs: []string{"a", "b"}}
	if err := ValidateSchedule(good); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Schedule){
		"no name":      func(s *Schedule) { s.Name = "" },
		"no anchor":    func(s *Schedule) { s.Anchor = time.Time{} },
		"shift 0":      func(s *Schedule) { s.ShiftHours = 0 },
		"shift huge":   func(s *Schedule) { s.ShiftHours = 721 },
		"no channels":  func(s *Schedule) { s.ChannelIDs = nil },
		"duplicate":    func(s *Schedule) { s.ChannelIDs = []string{"a", "a"} },
		"empty member": func(s *Schedule) { s.ChannelIDs = []string{"a", ""} },
	} {
		s := good
		mut(&s)
		if ValidateSchedule(s) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	both := []Step{{Step: 1, AfterMinutes: 5, ChannelID: "c", ScheduleID: "s"}}
	none := []Step{{Step: 1, AfterMinutes: 5}}
	if ValidateSteps(both) == nil || ValidateSteps(none) == nil {
		t.Error("a step needs exactly one of channel and schedule")
	}
	if err := ValidateSteps([]Step{{Step: 1, AfterMinutes: 5, ScheduleID: "s"}}); err != nil {
		t.Error(err)
	}
}
