package driver

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// fakePort answers Modbus requests from a register map, computing CRCs like a
// real RTU device. It lets the full frame path (request build, CRC verify,
// byte-count check, decode) run without hardware.
type fakePort struct {
	regs    map[int]uint16 // input/holding registers
	coils   map[int]bool
	sawAddr byte
	sawFn   byte
	gotReq  []byte
	readBuf *bytes.Reader
	corrupt bool
}

func (f *fakePort) SetMode(*serial.Mode) error         { return nil }
func (f *fakePort) Drain() error                       { return nil }
func (f *fakePort) ResetInputBuffer() error            { return nil }
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) Break(time.Duration) error          { return nil }
func (f *fakePort) Close() error                       { return nil }
func (f *fakePort) SetRTS(bool) error                  { return nil }
func (f *fakePort) ResetOutputBuffer() error           { return nil }
func (f *fakePort) SetDTR(bool) error                  { return nil }
func (f *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.gotReq = append([]byte(nil), p...)
	addr, fn := p[0], p[1]
	reg := int(binary.BigEndian.Uint16(p[2:4]))
	count := int(binary.BigEndian.Uint16(p[4:6]))
	f.sawAddr, f.sawFn = addr, fn
	var data []byte
	switch fn {
	case 1, 2:
		var b byte
		for i := 0; i < count; i++ {
			if f.coils[reg+i] {
				b |= 1 << i
			}
		}
		data = []byte{b}
	default:
		for i := 0; i < count; i++ {
			w := f.regs[reg+i]
			data = append(data, byte(w>>8), byte(w))
		}
	}
	resp := []byte{addr, fn, byte(len(data))}
	resp = append(resp, data...)
	c := crc16(resp)
	resp = append(resp, byte(c), byte(c>>8))
	if f.corrupt {
		resp[3] ^= 0xFF
	}
	f.readBuf = bytes.NewReader(resp)
	return len(p), nil
}

func (f *fakePort) Read(p []byte) (int, error) {
	if f.readBuf == nil {
		return 0, nil
	}
	return f.readBuf.Read(p)
}

func TestGenericDriverRoundTrip(t *testing.T) {
	fp := &fakePort{
		regs:  map[int]uint16{10: 0x42C8, 11: 0x0000, 20: 0x00FA},
		coils: map[int]bool{5: true},
	}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "s1", Address: 7, Points: []config.Point{
		{ID: "temp", Register: 10, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "C", Min: -50, Max: 150},
		{ID: "count", Register: 20, Func: 3, Type: "u16", Scale: 0.1, Unit: "l", Min: 0, Max: 100},
		{ID: "valve", Register: 5, Func: 1, Min: 0, Max: 1},
	}}}
	readings, err := d.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 3 {
		t.Fatalf("readings=%d", len(readings))
	}
	if readings[0].Value != 100 || readings[0].Unit != "C" {
		t.Fatalf("temp: %+v", readings[0])
	}
	if readings[1].Value != 25 { // 250 * 0.1
		t.Fatalf("count: %+v", readings[1])
	}
	if readings[2].Value != 1 {
		t.Fatalf("valve: %+v", readings[2])
	}
	if fp.sawAddr != 7 {
		t.Fatalf("addr %d", fp.sawAddr)
	}
	// request frame CRC must be correct
	req := fp.gotReq
	if crc16(req[:len(req)-2]) != binary.LittleEndian.Uint16(req[len(req)-2:]) {
		t.Fatal("request crc wrong")
	}
}

func TestGenericDriverRejectsCorruptFrame(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{10: 5}, corrupt: true}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "s1", Address: 1, Points: []config.Point{
		{ID: "x", Register: 10, Func: 4, Min: 0, Max: 100},
	}}}
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("corrupt frame accepted")
	}
}

func TestGenericDriverRejectsOutOfRange(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{10: 500}}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "s1", Address: 1, Points: []config.Point{
		{ID: "x", Register: 10, Func: 4, Min: 0, Max: 100},
	}}}
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("out-of-range value accepted")
	}
}
