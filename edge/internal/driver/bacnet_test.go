package driver

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// fakeBACnet answers unicast ReadProperty(present-value) from a table of
// object -> encoded application value. Missing objects get a BACnet Error PDU.
func fakeBACnet(t *testing.T, vals map[uint32][]byte, routed bool) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			p := buf[:n]
			if n < 15 || p[0] != 0x81 || p[4] != 1 || p[6] != 0 || p[9] != 0x0C {
				continue
			}
			invoke := p[8]
			obj := binary.BigEndian.Uint32(p[11:15])
			var apdu []byte
			if v, ok := vals[obj]; ok {
				apdu = append([]byte{0x30, invoke, 0x0C, 0x0C, byte(obj >> 24), byte(obj >> 16), byte(obj >> 8), byte(obj), 0x19, 85, 0x3E}, append(v, 0x3F)...)
			} else {
				apdu = []byte{0x50, invoke, 0x0C, 0x91, 0x01, 0x91, 0x1F} // Error: object / unknown-object
			}
			npdu := []byte{0x01, 0x00}
			if routed { // reply carrying a source specifier (device behind a router)
				npdu = []byte{0x01, 0x08, 0x00, 0x05, 0x01, 0x07}
			}
			msg := append([]byte{0x81, 0x0A, 0, 0}, append(npdu, apdu...)...)
			binary.BigEndian.PutUint16(msg[2:], uint16(len(msg)))
			pc.WriteTo(msg, from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func realTag(f float32) []byte {
	b := make([]byte, 5)
	b[0] = 0x44
	binary.BigEndian.PutUint32(b[1:], math.Float32bits(f))
	return b
}

func TestBACnetReadPresentValue(t *testing.T) {
	ai1 := uint32(0)<<22 | 1
	av7 := uint32(2)<<22 | 7
	bi3 := uint32(3)<<22 | 3
	mi2 := uint32(13)<<22 | 2
	ai9 := uint32(0)<<22 | 9
	for _, routed := range []bool{false, true} {
		port := fakeBACnet(t, map[uint32][]byte{
			ai1: realTag(21.5), av7: {0x21, 200}, bi3: {0x11}, mi2: {0x91, 3}, ai9: {0x32, 0xFF, 0xF6}, // real, unsigned, bool, enum, signed -10
		}, routed)
		d, err := New(config.Device{ID: "ahu", Profile: "bacnet", Host: "127.0.0.1", NetPort: port, Points: []config.Point{
			{ID: "supply_temp", Key: "ai:1", Unit: "C", Min: 0, Max: 100},
			{ID: "setpoint", Key: "AV:7", Scale: 0.1, Unit: "C", Min: 0, Max: 100},
			{ID: "fan", Key: "bi:3", Min: 0, Max: 1},
			{ID: "mode", Key: "mi:2", Min: 0, Max: 5},
			{ID: "delta", Key: "ai:9", Min: -50, Max: 50},
		}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Poll(context.Background())
		d.Close()
		if err != nil || len(got) != 5 {
			t.Fatalf("routed=%v %v %v", routed, err, got)
		}
		if got[0].Value != 21.5 || got[1].Value != 20 || got[2].Value != 1 || got[3].Value != 3 || got[4].Value != -10 {
			t.Fatalf("routed=%v values %+v", routed, got)
		}
	}
}

func TestBACnetErrorsAndConfig(t *testing.T) {
	port := fakeBACnet(t, map[uint32][]byte{uint32(0)<<22 | 1: realTag(500)}, false)
	// unknown object -> BACnet error, not a zero
	d, _ := New(config.Device{ID: "d", Profile: "bacnet", Host: "127.0.0.1", NetPort: port, Points: []config.Point{{ID: "x", Key: "ai:99", Min: 0, Max: 1}}})
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("BACnet error PDU accepted")
	}
	d.Close()
	// out of range
	d, _ = New(config.Device{ID: "d", Profile: "bacnet", Host: "127.0.0.1", NetPort: port, Points: []config.Point{{ID: "x", Key: "ai:1", Min: 0, Max: 100}}})
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("out-of-range accepted")
	}
	d.Close()
	for _, k := range []string{"", "ai", "xx:1", "ai:abc", "ai:5000000", "ai:-1"} {
		if _, err := New(config.Device{ID: "d", Profile: "bacnet", Host: "h", Points: []config.Point{{ID: "x", Key: k}}}); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
	if _, err := New(config.Device{ID: "d", Profile: "bacnet", Points: []config.Point{{ID: "x", Key: "ai:1"}}}); err == nil {
		t.Error("no host accepted")
	}
	// garbage and truncated replies never panic
	for _, g := range [][]byte{nil, {0x81}, {0x81, 0x0A, 0, 5, 1}, {0x81, 0x0A, 0, 9, 1, 0, 0x30, 1, 0x0C}} {
		parseBACnetReply(g, 1, 0)
	}
}
