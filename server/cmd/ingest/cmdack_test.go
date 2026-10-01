package main

import "testing"

func TestParseCmdAck(t *testing.T) {
	ok := `{"request_id":"r1","state":"acked","detail":"x"}`
	tn, gw, a, err := parseCmdAck("t/ten/g/gw1/cmd/ack", []byte(ok))
	if err != nil || tn != "ten" || gw != "gw1" || a.RequestID != "r1" {
		t.Fatalf("%v %s %s %+v", err, tn, gw, a)
	}
	bad := []struct{ topic, body string }{
		{"t/ten/g/gw1/cmd", ok},
		{"t/ten/g/gw1/cmd/ack/x", ok},
		{"t//g/gw1/cmd/ack", ok},
		{"t/ten/g/gw1/cmd/ack", `{"request_id":"","state":"acked"}`},
		{"t/ten/g/gw1/cmd/ack", `{"request_id":"r","state":"done"}`},
		{"t/ten/g/gw1/cmd/ack", `junk`},
	}
	for _, b := range bad {
		if _, _, _, err := parseCmdAck(b.topic, []byte(b.body)); err == nil {
			t.Errorf("accepted %q %q", b.topic, b.body)
		}
	}
}
