package discover

import "testing"

func TestParseIAm(t *testing.T) {
	// Built by hand from ASHRAE 135 clause 16.10: Device,1234; max-APDU 1476; no segmentation; vendor 5.
	iam := []byte{0x81, 0x0b, 0x00, 0x15, 0x01, 0x20, 0xff, 0xff, 0x00, 0xff, 0x10, 0x00, 0xC4, 0x02, 0x00, 0x04, 0xD2, 0x22, 0x05, 0xC4, 0x91, 0x03, 0x21, 0x05}
	d, err := ParseIAm(iam)
	if err != nil || d.Instance != 1234 || d.Vendor != 5 {
		t.Fatalf("%+v %v", d, err)
	}
	// source-specifier variant (router in between)
	r := []byte{0x81, 0x0a, 0x00, 0x1a, 0x01, 0x08, 0x00, 0x07, 0x01, 0x09, 0x10, 0x00, 0xC4, 0x02, 0x00, 0x00, 0x2A, 0x22, 0x05, 0xC4, 0x91, 0x03, 0x21, 0x05}
	if d, err := ParseIAm(r); err != nil || d.Instance != 42 {
		t.Fatalf("%+v %v", d, err)
	}
	for i := range iam {
		ParseIAm(iam[:i]) // truncation must never panic
	}
	notDev := append([]byte{}, iam...)
	notDev[13] = 0x00 // object type 0 (analog-input)
	if _, err := ParseIAm(notDev); err == nil {
		t.Error("non-device object accepted")
	}
	if _, err := ParseIAm(WhoIs); err == nil {
		t.Error("Who-Is parsed as I-Am")
	}
}
