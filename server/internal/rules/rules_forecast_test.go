package rules

import (
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/forecast"
)

func TestForecastLimitValidate(t *testing.T) {
	ok := Definition{Kind: "forecast_limit", DeviceID: "d", PointID: "p", Op: ">", Threshold: 80, HorizonHours: 12, Severity: "warning"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mut := range []func(*Definition){
		func(d *Definition) { d.DeviceID = "" },
		func(d *Definition) { d.Op = "=" },
		func(d *Definition) { d.HorizonHours = 0 },
		func(d *Definition) { d.HorizonHours = 100 },
	} {
		d := ok
		mut(&d)
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
}

func TestForecastCrossingDecision(t *testing.T) {
	fc := []forecast.Point{{Step: 1, Value: 70}, {Step: 2, Value: 79}, {Step: 3, Value: 85}, {Step: 4, Value: 95}}
	if st, hit := ForecastCrossing(fc, ">", 80, 4, true); !hit || st != 3 {
		t.Fatalf("step %d hit %v", st, hit)
	}
	if _, hit := ForecastCrossing(fc, ">", 80, 2, true); hit {
		t.Fatal("crossing beyond the horizon counted")
	}
	if _, hit := ForecastCrossing(fc, ">", 80, 4, false); hit {
		t.Fatal("unreliable model fired an alert")
	}
	if st, hit := ForecastCrossing(fc, "<", 72, 4, true); !hit || st != 1 {
		t.Fatalf("down crossing %d %v", st, hit)
	}
}
