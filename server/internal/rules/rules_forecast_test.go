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

func TestSeasonalValidateAndHit(t *testing.T) {
	ok := Definition{Kind: "seasonal", PointID: "kw", Sigma: 3, WindowDays: 14, Severity: "warning"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mut := range []func(*Definition){
		func(d *Definition) { d.WindowDays = 2 },
		func(d *Definition) { d.WindowDays = 29 },
		func(d *Definition) { d.Sigma = 1 },
		func(d *Definition) { d.Season = "month" },
		func(d *Definition) { d.PointID = "" },
	} {
		d := ok
		mut(&d)
		if d.Validate() == nil {
			t.Fatalf("accepted bad seasonal rule %+v", d)
		}
	}
	base := make([]float64, 40)
	for i := range base {
		base[i] = 100 + float64(i%3) // 100..102
	}
	if _, hit := SeasonalHit(base, 101, 3, "both"); hit {
		t.Fatal("usual value fired")
	}
	if _, hit := SeasonalHit(base, 130, 3, "both"); !hit {
		t.Fatal("far value did not fire")
	}
	if _, hit := SeasonalHit(base, 130, 3, "below"); hit {
		t.Fatal("above-value fired on a below-only rule")
	}
	if _, hit := SeasonalHit(base[:10], 500, 3, "both"); hit {
		t.Fatal("fired on too little history")
	}
}
