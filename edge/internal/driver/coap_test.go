package driver

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// fakeCoAP serves paths -> payloads. mode: "" normal piggybacked ACK, "drop1"
// ignores the first request (tests retransmit), "separate" sends an empty ACK
// then a CON response, "bad" answers 4.04.
func fakeCoAP(t *testing.T, res map[string]string, mode string) int {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		first := true
		for {
			n, a, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			b := append([]byte{}, buf[:n]...)
			if first && mode == "drop1" {
				first = false
				continue
			}
			tkl := int(b[0] & 0x0F)
			tok := b[4 : 4+tkl]
			var segs []string
			p, num := 4+tkl, 0
			for p < n && b[p] != 0xFF {
				d, l := int(b[p]>>4), int(b[p]&15)
				p++
				num += d
				if num == 11 {
					segs = append(segs, string(b[p:p+l]))
				}
				p += l
			}
			body, ok := res["/"+strings.Join(segs, "/")]
			code := byte(2<<5 | 5)
			if !ok || mode == "bad" {
				code, body = 4<<5|4, ""
			}
			resp := func(typ byte, mid0, mid1 byte) []byte {
				r := append([]byte{0x40 | typ<<4 | byte(tkl), code, mid0, mid1}, tok...)
				if body != "" {
					r = append(r, 0xFF)
					r = append(r, body...)
				}
				return r
			}
			if mode == "separate" {
				pc.WriteTo([]byte{0x60, 0, b[2], b[3]}, a)
				pc.WriteTo(resp(0, 0x99, 0x99), a)
				continue
			}
			pc.WriteTo(resp(2, b[2], b[3]), a)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func coapDev(port int) config.Device {
	return config.Device{ID: "s", Profile: "coap", Host: "127.0.0.1", NetPort: port, Points: []config.Point{
		{ID: "temp", Key: "/sensors/env#temp", Min: -40, Max: 125},
		{ID: "hum", Key: "/sensors/env#air.hum", Min: 0, Max: 100},
		{ID: "bat", Key: "/bat", Min: 0, Max: 5},
	}}
}

var coapRes = map[string]string{"/sensors/env": `{"temp":21.5,"air":{"hum":40}}`, "/bat": "3.1\n"}

func TestCoAPPoll(t *testing.T) {
	for _, mode := range []string{"", "drop1", "separate"} {
		d, err := New(coapDev(fakeCoAP(t, coapRes, mode)))
		if err != nil {
			t.Fatal(err)
		}
		rs, err := d.Poll(context.Background())
		if err != nil || len(rs) != 3 || rs[0].Value != 21.5 || rs[1].Value != 40 || rs[2].Value != 3.1 {
			t.Fatalf("%q: %v %v", mode, rs, err)
		}
	}
}

func TestCoAPErrors(t *testing.T) {
	d, _ := New(coapDev(fakeCoAP(t, coapRes, "bad")))
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "4.04") {
		t.Errorf("4.04: %v", err)
	}
	dev := coapDev(fakeCoAP(t, map[string]string{"/sensors/env": `{"temp":500}`, "/bat": "x"}, ""))
	d, _ = New(dev)
	if _, err := d.Poll(context.Background()); err == nil {
		t.Error("out of range accepted")
	}
	dev = coapDev(fakeCoAP(t, map[string]string{"/sensors/env": `{"temp":1,"air":{"hum":1}}`, "/bat": "abc"}, ""))
	d, _ = New(dev)
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "not a number") {
		t.Errorf("text: %v", err)
	}
	for _, mut := range []func(*config.Device){
		func(d *config.Device) { d.Host = "" },
		func(d *config.Device) { d.Points[0].Key = "temp" },
		func(d *config.Device) { d.Points = nil },
	} {
		dev := coapDev(1)
		mut(&dev)
		if _, err := New(dev); err == nil {
			t.Error("invalid config accepted")
		}
	}
}

func TestCoAPParseRobust(t *testing.T) {
	for _, b := range [][]byte{nil, {0x40}, {0x48, 1, 0, 0}, {0x40, 0x45, 0, 0, 0xD0}, {0x40, 0x45, 0, 0, 0xF0}} {
		if _, _, _, _, _, err := parseCoAP(b); err == nil {
			t.Errorf("accepted %x", b)
		}
	}
}
