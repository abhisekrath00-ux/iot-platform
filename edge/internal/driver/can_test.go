package driver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestCANDecodeVectors(t *testing.T) {
	cases := []struct {
		key  string
		data []byte
		want float64
	}{
		{"0x1:0:16:le:u", []byte{0x34, 0x12}, 0x1234},
		{"0x1:4:8:le:u", []byte{0xF0, 0x0F}, 255},
		{"0x1:0:16:le:s", []byte{0xFF, 0xFF}, -1},
		{"0x1:0:8:le:s", []byte{0x7F}, 127},
		{"0x1:7:16:be:u", []byte{0x12, 0x34}, 0x1234},
		{"0x1:3:8:be:u", []byte{0x0A, 0xB0}, 0xAB}, // crosses a byte: low nibble of byte 0, then high nibble of byte 1
		{"0x1:7:8:be:s", []byte{0x80}, -128},
		{"0x1:56:8:le:u", []byte{0, 0, 0, 0, 0, 0, 0, 0x9A}, 0x9A},
		{"0x1:0:32:le:u", []byte{0xFF, 0xFF, 0xFF, 0xFF}, 4294967295},
		{"0x1:0:1:le:u", []byte{0x01}, 1},
	}
	for _, c := range cases {
		s, err := parseCANKey(c.key)
		if err != nil {
			t.Fatalf("%s: %v", c.key, err)
		}
		got, err := s.decode(c.data)
		if err != nil || got != c.want {
			t.Errorf("%s % x: got %v (%v), want %v", c.key, c.data, got, err, c.want)
		}
	}
}

func TestCANKeyRejects(t *testing.T) {
	for _, k := range []string{"", "123:0:8:le:u", "0x123:0:8:xx:u", "0x123:0:0:le:u", "0x123:0:33:le:u", "0x123:60:8:le:u", "0x123:64:8:le:u", "0x3FFFFFFFF:0:8:le:u", "0x1:0:8:le:u;rm", "0x1:0:8:le"} {
		if _, err := parseCANKey(k); err == nil {
			t.Errorf("%q accepted", k)
		}
	}
}

type fakeCANSrc struct{ ch chan canFrame }

func (f *fakeCANSrc) Frames(ctx context.Context) <-chan canFrame { return f.ch }
func (f *fakeCANSrc) Close() error                               { return nil }

func canDev() config.Device {
	return config.Device{ID: "ecu", Profile: "can", Port: "vcan0", Points: []config.Point{
		{ID: "rpm", Key: "0x100:0:16:le:u", Scale: 0.25, Min: 0, Max: 8000, Unit: "rpm"},
		{ID: "temp", Key: "0x18FEEE00:0:8:le:u", Min: 0, Max: 255, Unit: "C"},
	}}
}

func waitFor(t *testing.T, d Driver, want int) ([]Reading, error) {
	t.Helper()
	var rs []Reading
	var err error
	for i := 0; i < 50; i++ {
		rs, err = d.Poll(context.Background())
		if err == nil && len(rs) == want {
			return rs, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return rs, err
}

func TestCANDriverPollsFromSimulatedBus(t *testing.T) {
	src := &fakeCANSrc{ch: make(chan canFrame, 8)}
	d, err := newCANWith(canDev(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "no recent frame") {
		t.Fatalf("poll before any frame: %v", err)
	}
	src.ch <- canFrame{ID: 0x100, Data: []byte{0x40, 0x1F}, At: time.Now()}           // 0x1F40 = 8000 raw -> 2000 rpm
	src.ch <- canFrame{ID: 0x18FEEE00, Ext: true, Data: []byte{90}, At: time.Now()}   // extended id
	src.ch <- canFrame{ID: 0x18FEEE00, Ext: false, Data: []byte{200}, At: time.Now()} // same number, standard frame: must be ignored
	rs, err := waitFor(t, d, 2)
	if err != nil || rs[0].Value != 2000 || rs[1].Value != 90 {
		t.Fatalf("%v %v", rs, err)
	}
}

func TestCANDriverRangeAndStale(t *testing.T) {
	src := &fakeCANSrc{ch: make(chan canFrame, 8)}
	d, _ := newCANWith(canDev(), src)
	defer d.Close()
	src.ch <- canFrame{ID: 0x100, Data: []byte{0xFF, 0xFF}, At: time.Now()} // 65535 * 0.25 > 8000
	src.ch <- canFrame{ID: 0x18FEEE00, Ext: true, Data: []byte{1}, At: time.Now()}
	time.Sleep(50 * time.Millisecond)
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("out of range: %v", err)
	}
	src2 := &fakeCANSrc{ch: make(chan canFrame, 8)}
	d2, _ := newCANWith(canDev(), src2)
	defer d2.Close()
	src2.ch <- canFrame{ID: 0x100, Data: []byte{1, 0}, At: time.Now().Add(-5 * time.Minute)}
	src2.ch <- canFrame{ID: 0x18FEEE00, Ext: true, Data: []byte{1}, At: time.Now()}
	time.Sleep(50 * time.Millisecond)
	if _, err := d2.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "no recent frame") {
		t.Fatalf("stale frame must not be reported as current: %v", err)
	}
}

func TestCANConfigRejects(t *testing.T) {
	for name, mut := range map[string]func(*config.Device){
		"bad iface": func(d *config.Device) { d.Port = "../etc/passwd" },
		"no points": func(d *config.Device) { d.Points = nil },
		"bad key":   func(d *config.Device) { d.Points[0].Key = "rpm" },
	} {
		dev := canDev()
		mut(&dev)
		if _, err := New(dev); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
