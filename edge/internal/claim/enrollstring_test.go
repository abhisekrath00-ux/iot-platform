package claim

import (
	"strings"
	"testing"
)

func TestEnrollStringRoundTrip(t *testing.T) {
	in := Enroll{API: "https://iot.plant.local:8443", Code: "ABCD-1234", Serial: "GW-7"}
	got, err := ParseEnroll("  " + EncodeEnroll(in) + "\n")
	if err != nil || got != in {
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
