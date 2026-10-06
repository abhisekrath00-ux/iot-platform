package claim

import (
	"strings"
	"testing"
)

func TestEnrollStringRoundTrip(t *testing.T) {
	in := Enroll{API: "https://iot.plant.local:8443", Code: "ABCD-1234", Serial: "GW-7"}
	got, err := ParseEnroll("  " + EncodeEnroll(in) + "\n")
	if err != nil || got.API != in.API || got.Code != in.Code || got.Serial != in.Serial || len(got.Fallbacks) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "x", "hexmon-enroll:1:!!!", "hexmon-enroll:1:e30",
		EncodeEnroll(Enroll{API: "ftp://h", Code: "c", Serial: "s"}),
		EncodeEnroll(Enroll{API: "https://u:p@h", Code: "c", Serial: "s"}),
		EncodeEnroll(Enroll{API: "https://h", Code: "", Serial: "s"}),
		strings.Replace(EncodeEnroll(in), "enroll:1", "enroll:9", 1)} {
		if _, err := ParseEnroll(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestEnrollFallbacksRoundTrip(t *testing.T) {
	s := EncodeEnroll(Enroll{API: "https://a.example", Code: "C", Serial: "S", Fallbacks: []string{"http://10.0.0.5:8000"}})
	e, err := ParseEnroll(s)
	if err != nil || len(e.URLs()) != 2 || e.URLs()[1] != "http://10.0.0.5:8000" {
		t.Fatalf("%+v %v", e, err)
	}
	bad := EncodeEnroll(Enroll{API: "https://a.example", Code: "C", Serial: "S", Fallbacks: []string{"ftp://x"}})
	if _, err := ParseEnroll(bad); err == nil {
		t.Fatal("bad fallback must be rejected")
	}
	old := EncodeEnroll(Enroll{API: "https://a.example", Code: "C", Serial: "S"})
	if e, err := ParseEnroll(old); err != nil || len(e.URLs()) != 1 {
		t.Fatalf("old format must keep working: %v", err)
	}
}
