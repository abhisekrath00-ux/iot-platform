package driver

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func writerFor(fp *fakePort, writes ...config.Point) *modbusGeneric {
	return newModbusGeneric(fp, config.Device{ID: "vfd1", Address: 3, Writes: writes})
}

func TestModbusWriteSingleRegisterScaled(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{}}
	g := writerFor(fp, config.Point{ID: "setpoint", Register: 100, Func: 6, Type: "u16", Scale: 0.1, Min: 0, Max: 60})
	if err := g.Write(context.Background(), "setpoint", 45.5); err != nil {
		t.Fatal(err)
	}
	if fp.regs[100] != 455 || len(fp.writes) != 1 || fp.writes[0][0] != 3 || fp.writes[0][1] != 6 {
		t.Fatalf("regs=%v writes=%x", fp.regs, fp.writes)
	}
	req := fp.writes[0]
	if crc16(req[:len(req)-2]) != binary.LittleEndian.Uint16(req[len(req)-2:]) {
		t.Fatal("bad request crc")
	}
}

func TestModbusWriteFloat32WordOrders(t *testing.T) {
	for order, want := range map[string][2]uint16{"abcd": {0x42C8, 0}, "cdab": {0, 0x42C8}, "badc": {0xC842, 0}, "dcba": {0, 0xC842}} {
		fp := &fakePort{regs: map[int]uint16{}}
		g := writerFor(fp, config.Point{ID: "sp", Register: 10, Func: 16, Type: "f32", WordOrder: order, Min: 0, Max: 200})
		if err := g.Write(context.Background(), "sp", 100); err != nil {
			t.Fatalf("%s: %v", order, err)
		}
		if fp.regs[10] != want[0] || fp.regs[11] != want[1] {
			t.Fatalf("%s: got %x %x want %x", order, fp.regs[10], fp.regs[11], want)
		}
	}
	// the encoder is the exact inverse of the reader's decoder
	w, _ := encodeWords(0x449A43D7, "cdab")
	if v, _ := decodeWords(w, "f32", "cdab"); v < 1234.11 || v > 1234.13 {
		t.Fatalf("roundtrip %v", v)
	}
}

func TestModbusWriteCoil(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{}}
	g := writerFor(fp, config.Point{ID: "relay", Register: 7, Func: 5, Min: 0, Max: 1})
	if err := g.Write(context.Background(), "relay", 1); err != nil {
		t.Fatal(err)
	}
	if fp.writes[0][4] != 0xFF {
		t.Fatalf("coil on frame %x", fp.writes[0])
	}
	if err := g.Write(context.Background(), "relay", 0.5); err == nil {
		t.Fatal("non 0/1 coil value accepted")
	}
}

// Everything that must never reach the wire.
func TestModbusWriteRefusals(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{}}
	g := writerFor(fp,
		config.Point{ID: "sp", Register: 100, Func: 6, Type: "u16", Min: 0, Max: 100},
		config.Point{ID: "norange", Register: 101, Func: 6, Type: "u16"},
		config.Point{ID: "badfn", Register: 102, Func: 3, Min: 0, Max: 1},
		config.Point{ID: "wide", Register: 103, Func: 6, Type: "f32", Min: 0, Max: 1},
	)
	ctx := context.Background()
	for name, c := range map[string]struct {
		id string
		v  float64
	}{
		"not allowlisted": {"other", 1}, "above max": {"sp", 101}, "below min": {"sp", -1},
		"no range": {"norange", 1}, "bad function": {"badfn", 1}, "fn6 with 32-bit": {"wide", 1},
	} {
		if err := g.Write(ctx, c.id, c.v); err == nil {
			t.Errorf("%s: write accepted", name)
		}
	}
	nan := 0.0
	nan = nan / nan
	if err := g.Write(ctx, "sp", nan); err == nil {
		t.Error("NaN accepted")
	}
	if len(fp.writes) != 0 {
		t.Fatalf("%d frames reached the wire", len(fp.writes))
	}
}

func TestModbusWriteDeviceFailures(t *testing.T) {
	pt := config.Point{ID: "sp", Register: 100, Func: 6, Type: "u16", Min: 0, Max: 100}
	fp := &fakePort{regs: map[int]uint16{}, exception: 2}
	if err := writerFor(fp, pt).Write(context.Background(), "sp", 5); err == nil || !strings.Contains(err.Error(), "exception code 2") {
		t.Fatalf("exception: %v", err)
	}
	fp = &fakePort{regs: map[int]uint16{}, badEcho: true}
	if err := writerFor(fp, pt).Write(context.Background(), "sp", 5); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("bad echo: %v", err)
	}
}

func TestModbusTCPWriteEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req := make([]byte, 12) // MBAP(7) + fn6 pdu(5)
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		got <- req
		resp := append([]byte{req[0], req[1], 0, 0, 0, 6, req[6]}, req[7:12]...)
		c.Write(resp)
	}()
	a := ln.Addr().(*net.TCPAddr)
	d, err := NewWithOpener(nil, config.Device{ID: "plc", Profile: "modbus-tcp", Host: "127.0.0.1", NetPort: a.Port, Address: 1,
		Writes: []config.Point{{ID: "sp", Register: 20, Func: 6, Type: "u16", Min: 0, Max: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.(Writer).Write(context.Background(), "sp", 42); err != nil {
		t.Fatal(err)
	}
	req := <-got
	if req[7] != 6 || binary.BigEndian.Uint16(req[8:]) != 20 || binary.BigEndian.Uint16(req[10:]) != 42 || binary.BigEndian.Uint16(req[4:]) != 6 {
		t.Fatalf("frame %x", req)
	}
}
