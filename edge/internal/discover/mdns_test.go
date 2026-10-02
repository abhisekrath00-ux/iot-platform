package discover

import (
	"encoding/binary"
	"net"
	"testing"
)

// The response below is built by hand here, independently of the server's
// builder, with a compressed name pointer for the SRV target to exercise that path.
func rr(owner []byte, typ uint16, rdata []byte) []byte {
	b := append([]byte{}, owner...)
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint32(b, 120)
	b = binary.BigEndian.AppendUint16(b, uint16(len(rdata)))
	return append(b, rdata...)
}

func testResponse(port int, ip net.IP) []byte {
	h := make([]byte, 12)
	h[2] = 0x84
	binary.BigEndian.PutUint16(h[6:], 4)
	svc := encodeName(Service)
	inst := encodeName("hexmon." + Service)
	host := encodeName("iot-server.local.")
	srv := []byte{0, 0, 0, 0, byte(port >> 8), byte(port)}
	srv = append(srv, host...)
	out := append(h, rr(svc, 12, inst)...)
	out = append(out, rr(inst, 33, srv)...)
	out = append(out, rr(inst, 16, []byte{12, 's', 'c', 'h', 'e', 'm', 'e', '=', 'h', 't', 't', 'p', 's'})...)
	out = append(out, rr(host, 1, ip.To4())...)
	return out
}

func TestQueryAndResponseRoundTrip(t *testing.T) {
	q := BuildQuery()
	if len(q) < 12+len(Service) || q[5] != 1 {
		t.Fatalf("query %x", q)
	}
	ss, err := ParseResponse(testResponse(8443, net.ParseIP("10.0.0.5")), "10.0.0.5")
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %v", ss, err)
	}
	s := ss[0]
	if s.Port != 8443 || len(s.IPs) != 1 || s.IPs[0] != "10.0.0.5" || s.Host != "iot-server.local" || s.URL() != "https://10.0.0.5:8443" {
		t.Fatalf("%+v %s", s, s.URL())
	}
}

func TestParseResponseRejectsGarbage(t *testing.T) {
	good := testResponse(8443, net.ParseIP("10.0.0.5"))
	for i := range good {
		// every truncation must fail cleanly, never panic
		ParseResponse(good[:i], "x")
	}
	loop := append(make([]byte, 12), 0xC0, 12) // name pointer to itself
	loop[7] = 1
	if _, err := ParseResponse(append([]byte{0, 0, 0x84, 0, 0, 0, 0, 1, 0, 0, 0, 0}, 0xC0, 12, 0, 12, 0, 1, 0, 0, 0, 1, 0, 0), "x"); err == nil {
		t.Error("pointer loop / truncated record accepted")
	}
	if _, err := ParseResponse(BuildQuery(), "x"); err == nil {
		t.Error("query parsed as response")
	}
	// a SRV with port 0 is ignored
	if ss, _ := ParseResponse(testResponse(0, net.ParseIP("10.0.0.5")), "x"); len(ss) != 0 {
		t.Errorf("port 0 accepted: %+v", ss)
	}
	_ = loop
}
