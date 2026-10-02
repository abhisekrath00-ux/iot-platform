package driver

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestDNPCRCKnownVector(t *testing.T) {
	// Published example header: 05 64 05 C0 01 00 00 04 -> CRC 0x21E9 (bytes E9 21).
	if got := dnpCRC([]byte{0x05, 0x64, 0x05, 0xC0, 0x01, 0x00, 0x00, 0x04}); got != 0x21E9 {
		t.Fatalf("crc %04x", got)
	}
}

// fakeDNP3 answers any class 0 READ with the given application objects, split into
// two fragments when split is set.
func fakeDNP3(t *testing.T, src uint16, iin uint16, objs []byte, split bool) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					_, _, user, err := readDNPFrame(c)
					if err != nil {
						return
					}
					if len(user) < 6 || user[2] != 0x01 {
						continue // confirms etc.
					}
					seq := user[1] & 0x0F
					resp := func(appCtrl byte, body []byte) {
						app := append([]byte{appCtrl | seq, 0x81, byte(iin), byte(iin >> 8)}, body...)
						// transport segments of up to 249 bytes
						for first := true; len(app) > 0; first = false {
							n := len(app)
							if n > 249 {
								n = 249
							}
							th := byte(0)
							if first {
								th |= 0x40
							}
							if n == len(app) {
								th |= 0x80
							}
							c.Write(dnpFrame(1, src, 0x44, append([]byte{th}, app[:n]...)))
							app = app[n:]
						}
					}
					if split {
						half := len(objs) / 2
						resp(0x80|0x20, objs[:half]) // FIR, CON
						_ = half
						// second fragment continues objects: send as separate complete object blocks instead
						resp(0x40|0x80, objs[half:])
						continue
					}
					resp(0xC0, objs)
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func dnpObjs() []byte {
	var b []byte
	// g30 v5 float, range 0-1 (2 points, qualifier 00)
	b = append(b, 30, 5, 0x00, 0, 1)
	for _, f := range []float32{230.5, 4.25} {
		b = append(b, 0x01)
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(f))
	}
	// g1 v2 binary, index 3 (online + state bit 7)
	b = append(b, 1, 2, 0x00, 3, 3, 0x81)
	// g20 v1 counter 32-bit with flag, index 0, qualifier 01 (2 byte range)
	b = append(b, 20, 1, 0x01, 0, 0, 0, 0, 0x01)
	b = binary.LittleEndian.AppendUint32(b, 123456)
	return b
}

func dnpDev(port int) config.Device {
	return config.Device{ID: "rtu", Profile: "dnp3", Host: "127.0.0.1", NetPort: port, Address: 10, Points: []config.Point{
		{ID: "volts", Key: "ai", Register: 0, Min: 0, Max: 500},
		{ID: "amps", Key: "ai", Register: 1, Min: 0, Max: 100},
		{ID: "breaker", Key: "bi", Register: 3, Min: 0, Max: 1},
		{ID: "kwh", Key: "ctr", Register: 0, Min: 0, Max: 1e9},
	}}
}

func TestDNP3ClassZeroPoll(t *testing.T) {
	port := fakeDNP3(t, 10, 0, dnpObjs(), false)
	d, err := New(dnpDev(port))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 2; i++ { // second poll reuses the connection and advances the sequence
		rs, err := d.Poll(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]float64{"volts": 230.5, "amps": 4.25, "breaker": 1, "kwh": 123456}
		for _, r := range rs {
			if want[r.PointID] != r.Value {
				t.Errorf("%s=%v", r.PointID, r.Value)
			}
		}
		if len(rs) != 4 {
			t.Fatalf("got %d", len(rs))
		}
	}
}

func TestDNP3MultiFragment(t *testing.T) {
	// split at an object boundary: first fragment = analog block, second = rest
	a := dnpObjs()
	split := 5 + 10 // header(5) + 2*5 bytes
	port := fakeDNP3(t, 10, 0, a, false)
	_ = port
	ln := fakeDNP3Frags(t, 10, a[:split], a[split:])
	d, _ := New(dnpDev(ln))
	defer d.Close()
	rs, err := d.Poll(context.Background())
	if err != nil || len(rs) != 4 {
		t.Fatalf("%v %v", rs, err)
	}
}

func fakeDNP3Frags(t *testing.T, src uint16, f1, f2 []byte) int {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _, user, err := readDNPFrame(c)
		if err != nil {
			return
		}
		seq := user[1] & 0x0F
		c.Write(dnpFrame(1, src, 0x44, append([]byte{0xC0, 0x40 | 0x20 | seq, 0x81, 0, 0}, f1...)))
		_, _, _, _ = readDNPFrame(c) // master's CONFIRM
		c.Write(dnpFrame(1, src, 0x44, append([]byte{0xC1, 0x40 | 0x80 | seq, 0x81, 0, 0}, f2...)))
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestDNP3Rejections(t *testing.T) {
	// offline flag
	bad := append([]byte{}, dnpObjs()...)
	bad[5] = 0x00 // volts flag: not ONLINE
	d, _ := New(dnpDev(fakeDNP3(t, 10, 0, bad, false)))
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline: %v", err)
	}
	// IIN2 object unknown
	d, _ = New(dnpDev(fakeDNP3(t, 10, 0x0200, dnpObjs(), false)))
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("iin: %v", err)
	}
	// out of range
	dev := dnpDev(fakeDNP3(t, 10, 0, dnpObjs(), false))
	dev.Points[0].Max = 100
	d, _ = New(dev)
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("range: %v", err)
	}
	// missing point
	dev = dnpDev(fakeDNP3(t, 10, 0, dnpObjs(), false))
	dev.Points = append(dev.Points, config.Point{ID: "x", Key: "ai", Register: 9, Max: 1})
	d, _ = New(dev)
	if _, err := d.Poll(context.Background()); err == nil {
		t.Error("missing point accepted")
	}
	// config validation
	for _, mut := range []func(*config.Device){
		func(d *config.Device) { d.Host = "" },
		func(d *config.Device) { d.Points[0].Key = "zz" },
		func(d *config.Device) { d.Points[1].Register = 0 },
		func(d *config.Device) { d.Points = nil },
	} {
		dev := dnpDev(1)
		mut(&dev)
		if _, err := New(dev); err == nil {
			t.Error("invalid config accepted")
		}
	}
}

func TestDNP3CorruptCRC(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		defer c.Close()
		readDNPFrame(c)
		f := dnpFrame(1, 10, 0x44, append([]byte{0xC0, 0xC0, 0x81, 0, 0}, dnpObjs()...))
		f[12] ^= 0xFF // corrupt a data byte
		c.Write(f)
	}()
	d, _ := New(dnpDev(ln.Addr().(*net.TCPAddr).Port))
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Fatalf("%v", err)
	}
}
