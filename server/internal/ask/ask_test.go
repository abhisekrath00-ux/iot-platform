package ask

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Query{
		"Open alerts?":                      {Kind: OpenAlerts},
		"critical alerts":                   {Kind: OpenAlerts, Severity: "critical"},
		"  Show   warning alerts ":          {Kind: OpenAlerts, Severity: "warning"},
		"alerts on Boiler-1":                {Kind: DeviceAlerts, Device: "boiler-1"},
		"critical alerts for Pump house 2":  {Kind: DeviceAlerts, Severity: "critical", Device: "pump house 2"},
		"latest temperature of boiler-1":    {Kind: LatestValue, Point: "temperature", Device: "boiler-1"},
		"What is the current temp for m 1?": {Kind: LatestValue, Point: "temp", Device: "m 1"},
		"offline devices":                   {Kind: OfflineDevice},
		"Which devices are offline?":        {Kind: OfflineDevice},
		"devices tagged boiler":             {Kind: TaggedDevices, Tag: "boiler"},
	}
	for in, want := range cases {
		got, ok := Parse(in)
		if !ok || got != want {
			t.Errorf("%q: got %+v ok=%v, want %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "delete everything", "turn off the siren", "write 5 to boiler-1", "alerts", "drop table devices", "critical alerts; select 1"} {
		if in == "alerts" {
			continue // "alerts" alone is a valid question (open alerts)
		}
		if q, ok := Parse(in); ok {
			t.Errorf("%q must not be understood, got %+v", in, q)
		}
	}
	if _, ok := Parse(string(make([]byte, 300))); ok {
		t.Error("overlong input")
	}
	// the question can never name an action: nothing but the read-only kinds exists
	for _, k := range []Kind{OpenAlerts, DeviceAlerts, LatestValue, OfflineDevice, TaggedDevices} {
		if Describe(Query{Kind: k, Device: "d", Point: "p", Tag: "t"}) == "" {
			t.Errorf("no description for %s", k)
		}
	}
}
