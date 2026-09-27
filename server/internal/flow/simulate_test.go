package flow

import (
	"testing"
	"time"
)

func simDef() Definition {
	return Definition{
		Trigger: Trigger{DeviceID: "d1", PointID: "temp", Op: ">", Value: 50},
		Steps: []Step{
			{Type: "condition", Op: "<", Value: 90},
			{Type: "delay", Seconds: 60},
			{Type: "notify", ChannelID: "ch1", Message: "temp {value} high"},
		},
	}
}

func TestSimulateCountsFirings(t *testing.T) {
	now := time.Now()
	readings := []Reading{
		{now, 40}, // below trigger
		{now, 60}, // fires
		{now, 95}, // above condition ceiling
		{now, 70}, // fires
	}
	ev := Simulate(simDef(), readings)
	if len(ev) != 2 {
		t.Fatalf("events=%d want 2 (%+v)", len(ev), ev)
	}
	if ev[0].Value != 60 || ev[1].Value != 70 {
		t.Fatalf("wrong events %+v", ev)
	}
	if ev[0].DelaySeconds != 60 {
		t.Fatalf("delay %d", ev[0].DelaySeconds)
	}
	if ev[0].Messages[0] != "ch1: temp 60 high" {
		t.Fatalf("message %q", ev[0].Messages[0])
	}
}

func TestSimulateEmpty(t *testing.T) {
	if ev := Simulate(simDef(), nil); len(ev) != 0 {
		t.Fatalf("%+v", ev)
	}
}
