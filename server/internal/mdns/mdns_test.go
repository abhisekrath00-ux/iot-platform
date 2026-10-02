package mdns

import (
	"net"
	"testing"
)

func query(svc string, typ byte) []byte {
	q := make([]byte, 12)
	q[5] = 1
	q = append(q, name(svc)...)
	return append(q, 0, typ, 0, 1)
}

func TestIsQuery(t *testing.T) {
	if !IsQuery(query(Service, 12)) {
		t.Error("PTR query for our service not recognised")
	}
	for _, p := range [][]byte{nil, query("_other._tcp.local.", 12), query(Service, 1), BuildResponse("x", "h", 1, nil, nil), {0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xC0}} {
		if IsQuery(p) {
			t.Errorf("accepted %x", p)
		}
	}
}

func TestBuildResponseHasAllRecords(t *testing.T) {
	r := BuildResponse("hexmon", "iot-server", 8443, []net.IP{net.ParseIP("10.0.0.5")}, []string{"scheme=https"})
	if r[2]&0x80 == 0 || int(r[7]) != 4 {
		t.Fatalf("header %x", r[:12])
	}
}
