package report

import "testing"

func TestMultiValueDeviceParam(t *testing.T) {
	base := Definition{Metrics: []Metric{{DeviceID: "a", PointID: "t"}, {DeviceID: "a", PointID: "h"}}, WindowHours: 24, GroupBy: "hour"}
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == "device" {
				return v
			}
			return ""
		}
	}
	d, err := ApplyParams(base, get("x,y,x"))
	if err != nil || len(d.Metrics) != 4 || d.Metrics[0].DeviceID != "x" || d.Metrics[2].DeviceID != "y" || d.Metrics[3].PointID != "h" {
		t.Fatalf("expand: %v %+v", err, d.Metrics)
	}
	if len(base.Metrics) != 2 || base.Metrics[0].DeviceID != "a" {
		t.Fatal("stored definition mutated")
	}
	if d, err = ApplyParams(base, get("z")); err != nil || len(d.Metrics) != 2 || d.Metrics[0].DeviceID != "z" {
		t.Fatalf("single: %v", err)
	}
	for _, bad := range []string{"x,Bad Id", "x,,y", ",", "a,b,c,d,e,f,g,h,i,j,k"} {
		if _, err := ApplyParams(base, get(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	// 10 devices x 6 points = 60 metrics exceeds the 50 metric cap: Validate must reject it
	many := Definition{WindowHours: 24, GroupBy: "hour"}
	for _, p := range []string{"p1", "p2", "p3", "p4", "p5", "p6"} {
		many.Metrics = append(many.Metrics, Metric{DeviceID: "a", PointID: p})
	}
	if _, err := ApplyParams(many, get("a,b,c,d,e,f,g,h,i,j")); err == nil {
		t.Fatal("metric cap bypassed")
	}
}
