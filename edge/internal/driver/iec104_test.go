package driver

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func apdu(vs, vr uint16, asdu []byte) []byte {
	b := []byte{0x68, byte(4 + len(asdu)), 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(b[2:], vs<<1)
	binary.LittleEndian.PutUint16(b[4:], vr<<1)
	return append(b, asdu...)
}

func ioa3(n int) []byte { return []byte{byte(n), byte(n >> 8), byte(n >> 16)} }

// fakeOutstation answers STARTDT and general interrogation with a mix of
// monitor types, then ACT_TERM (or a negative confirm when reject is set).
func fakeOutstation(t *testing.T, ca int, reject bool, extra func() [][]byte) int {
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
				var vs uint16
				for {
					h := make([]byte, 2)
					if _, err := io.ReadFull(c, h); err != nil {
						return
					}
					body := make([]byte, h[1])
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					if body[0] == 0x07 {
						c.Write([]byte{0x68, 4, 0x0B, 0, 0, 0})
						continue
					}
					if body[0]&1 == 0 && len(body) > 4 && body[4] == 100 { // interrogation
						cab := []byte{byte(ca), byte(ca >> 8)}
						if reject {
							c.Write(apdu(vs, 0, append([]byte{100, 1, 0x47, 0}, append(cab, 0, 0, 0, 20)...)))
							return
						}
						send := func(a []byte) { c.Write(apdu(vs, 1, a)); vs++ }
						send(append([]byte{100, 1, 7, 0}, append(cab, 0, 0, 0, 20)...)) // act con
						// M_ME_NC_1 (13): one float, IOA 4001 = 230.5, quality 0
						f := make([]byte, 4)
						binary.LittleEndian.PutUint32(f, math.Float32bits(230.5))
						send(append(append([]byte{13, 1, 20, 0}, cab...), append(append(ioa3(4001), f...), 0)...))
						// M_ME_NB_1 (11), sequence of 2 starting at IOA 100: scaled 250, 300
						send(append(append([]byte{11, 0x82, 20, 0}, cab...), append(append(ioa3(100), 250, 0, 0), 44, 1, 0)...))
						// M_SP_NA_1 (1): IOA 7 = on
						send(append(append([]byte{1, 1, 20, 0}, cab...), append(ioa3(7), 1)...))
						// M_ME_NC_1 flagged invalid (IV): IOA 4002
						binary.LittleEndian.PutUint32(f, math.Float32bits(99))
						send(append(append([]byte{13, 1, 20, 0}, cab...), append(append(ioa3(4002), f...), 0x80)...))
						// another station's data must be ignored
						send(append(append([]byte{13, 1, 20, 0}, byte(ca+1), 0), append(append(ioa3(4001), f...), 0)...))
						send(append([]byte{100, 1, 10, 0}, append(cab, 0, 0, 0, 20)...)) // act term
					}
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func iecDev(port int, pts ...config.Point) config.Device {
	return config.Device{ID: "rtu", Profile: "iec104", Host: "127.0.0.1", NetPort: port, Address: 1, Points: pts}
}

func TestIEC104GeneralInterrogation(t *testing.T) {
	port := fakeOutstation(t, 1, false, nil)
	d, err := New(iecDev(port,
		config.Point{ID: "volts", IOA: 4001, Unit: "V", Min: 0, Max: 500},
		config.Point{ID: "temp", IOA: 100, Scale: 0.1, Unit: "C", Min: 0, Max: 100},  // scaled 250 -> 25.0
		config.Point{ID: "temp2", IOA: 101, Scale: 0.1, Unit: "C", Min: 0, Max: 100}, // sequence element: 300 -> 30.0
		config.Point{ID: "breaker", IOA: 7, Min: 0, Max: 1},
	))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 4 {
		t.Fatalf("%v %v", err, got)
	}
	if got[0].Value != 230.5 || got[1].Value != 25 || got[2].Value != 30 || got[3].Value != 1 {
		t.Fatalf("values %+v", got)
	}
	// second poll on the same connection also works
	if _, err := d.Poll(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
}

func TestIEC104RefusalsAndBadData(t *testing.T) {
	port := fakeOutstation(t, 1, false, nil)
	// invalid quality (IV) is an error, never a number
	d, _ := New(iecDev(port, config.Point{ID: "x", IOA: 4002, Min: 0, Max: 500}))
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("IV-flagged value accepted")
	}
	d.Close()
	// missing IOA
	d, _ = New(iecDev(port, config.Point{ID: "x", IOA: 9999, Min: 0, Max: 1}))
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("missing IOA accepted")
	}
	d.Close()
	// out of range
	d, _ = New(iecDev(port, config.Point{ID: "x", IOA: 4001, Min: 0, Max: 100}))
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("out-of-range accepted")
	}
	d.Close()
	// negative confirmation from the outstation
	rp := fakeOutstation(t, 1, true, nil)
	d, _ = New(iecDev(rp, config.Point{ID: "x", IOA: 4001, Min: 0, Max: 500}))
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("negative confirm accepted")
	}
	d.Close()
	// config validation
	for name, dev := range map[string]config.Device{
		"no host":   {ID: "d", Profile: "iec104", Address: 1, Points: []config.Point{{ID: "a", IOA: 1}}},
		"no CA":     {ID: "d", Profile: "iec104", Host: "h", Points: []config.Point{{ID: "a", IOA: 1}}},
		"dup ioa":   {ID: "d", Profile: "iec104", Host: "h", Address: 1, Points: []config.Point{{ID: "a", IOA: 1}, {ID: "b", IOA: 1}}},
		"zero ioa":  {ID: "d", Profile: "iec104", Host: "h", Address: 1, Points: []config.Point{{ID: "a"}}},
		"no points": {ID: "d", Profile: "iec104", Host: "h", Address: 1},
	} {
		if _, err := New(dev); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestIEC104ParseTruncatedASDU(t *testing.T) {
	if _, _, _, _, err := parseASDU([]byte{13, 1, 20, 0, 1, 0, 1, 0, 0, 1}); err == nil {
		t.Fatal("truncated ASDU accepted")
	}
}
