package flow

import (
	"testing"
	"time"
)

func base() Definition {
	return Definition{
		Trigger: Trigger{DeviceID: "meter-1", PointID: "kwh", Op: ">", Value: 100},
		Steps: []Step{
			{Type: "condition", Op: "<", Value: 500},
			{Type: "delay", Seconds: 30},
			{Type: "notify", ChannelID: "ch1", Message: "usage {value} kWh high"},
		},
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(base()); err != nil {
		t.Fatal(err)
	}
	bad := base()
	bad.Trigger.Op = "~"
	if Validate(bad) == nil {
		t.Fatal("bad op accepted")
	}
	noNotify := base()
	noNotify.Steps = []Step{{Type: "delay", Seconds: 5}}
	if Validate(noNotify) == nil {
		t.Fatal("notify-less flow accepted")
	}
	longDelay := base()
	longDelay.Steps[1].Seconds = 7200
	if Validate(longDelay) == nil {
		t.Fatal("over-long delay accepted")
	}
}

func TestRunTriggerGate(t *testing.T) {
	d := base()
	if _, _, ok := Run(d, 50); ok {
		t.Fatal("trigger should not fire below threshold")
	}
}

func TestRunConditionStops(t *testing.T) {
	d := base()
	if _, _, ok := Run(d, 900); ok { // passes trigger (>100), fails condition (<500)
		t.Fatal("condition should stop run")
	}
}

func TestRunNotifyAndDelay(t *testing.T) {
	d := base()
	actions, wait, ok := Run(d, 250)
	if !ok || len(actions) != 1 {
		t.Fatalf("actions=%v ok=%v", actions, ok)
	}
	if wait != 30*time.Second {
		t.Fatalf("wait %v", wait)
	}
	if actions[0].ChannelID != "ch1" || actions[0].Message != "usage 250 kWh high" {
		t.Fatalf("message %q", actions[0].Message)
	}
}

func TestCooldown(t *testing.T) {
	now := time.Now()
	if InCooldown(time.Time{}, now, 60) || InCooldown(now.Add(-time.Second), now, 0) {
		t.Fatal("no history or zero cooldown must not suppress")
	}
	if !InCooldown(now.Add(-30*time.Second), now, 60) {
		t.Fatal("30s after notify with 60s cooldown must suppress")
	}
	if InCooldown(now.Add(-61*time.Second), now, 60) {
		t.Fatal("after cooldown must not suppress")
	}
	d := Definition{Trigger: Trigger{DeviceID: "d", PointID: "p", Op: ">", Value: 1}, Steps: []Step{{Type: "notify", ChannelID: "c"}}}
	d.CooldownSeconds = 86401
	if Validate(d) == nil {
		t.Fatal("cooldown over 24h accepted")
	}
	d.CooldownSeconds = -1
	if Validate(d) == nil {
		t.Fatal("negative cooldown accepted")
	}
	d.CooldownSeconds = 300
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
}

func TestLatchHolds(t *testing.T) {
	cases := []struct {
		latch bool
		last  string
		want  bool
	}{
		{true, "", false},
		{true, "notified", true},
		{true, "suppressed_latch", true},
		{true, "skipped_condition", false}, // re-armed
		{true, "suppressed_cooldown", false},
		{false, "notified", false},
	}
	for _, c := range cases {
		if got := LatchHolds(c.latch, c.last); got != c.want {
			t.Errorf("LatchHolds(%v,%q)=%v want %v", c.latch, c.last, got, c.want)
		}
	}
}
