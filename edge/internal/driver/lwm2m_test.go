package driver

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func lwDev(port int) config.Device {
	return config.Device{ID: "lw", Profile: "lwm2m", Host: "127.0.0.1", NetPort: port, Points: []config.Point{
		{ID: "temp", Key: "/3303/0/5700", Min: -40, Max: 125, Unit: "C"},
		{ID: "hum", Key: "/3304/0/5700", Min: 0, Max: 100, Scale: 1},
	}}
}

func TestLwM2MPollAndAcceptOption(t *testing.T) {
	d, err := New(lwDev(fakeCoAP(t, map[string]string{"/3303/0/5700": "21.5", "/3304/0/5700": "40"}, "")))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := d.Poll(context.Background())
	if err != nil || len(rs) != 2 || rs[0].Value != 21.5 || rs[1].Value != 40 {
		t.Fatalf("%v %v", rs, err)
	}
	// the request must ask for text/plain (Accept = option 17, zero-length) after the Uri-Path options
	req := coapGet(1, []byte{1, 2}, "/3303/0/5700", true)
	if req[len(req)-1] != 0x60 && req[len(req)-2] != 0x60 { // delta 6 (17-11), length 0
		t.Fatalf("no Accept option at the end: % x", req)
	}
	if plain := coapGet(1, []byte{1, 2}, "/3303/0/5700"); len(plain) != len(req)-1 {
		t.Fatalf("plain coap request must not carry Accept: % x vs % x", plain, req)
	}
}

func TestLwM2MRejectsBadConfig(t *testing.T) {
	for name, mut := range map[string]func(*config.Device){
		"no host":      func(d *config.Device) { d.Host = "" },
		"no points":    func(d *config.Device) { d.Points = nil },
		"plain path":   func(d *config.Device) { d.Points[0].Key = "/temp" },
		"two segments": func(d *config.Device) { d.Points[0].Key = "/3303/0" },
		"field suffix": func(d *config.Device) { d.Points[0].Key = "/3303/0/5700#v" },
		"traversal":    func(d *config.Device) { d.Points[0].Key = "/3303/../5700" },
	} {
		dev := lwDev(5683)
		mut(&dev)
		if _, err := New(dev); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLwM2MRangeCheck(t *testing.T) {
	d, _ := New(lwDev(fakeCoAP(t, map[string]string{"/3303/0/5700": "900", "/3304/0/5700": "40"}, "")))
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("out-of-range value accepted")
	}
}
