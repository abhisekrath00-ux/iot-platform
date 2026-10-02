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

func f32(f float32) []byte {
	return append([]byte{0x87, 5, 8}, binary.BigEndian.AppendUint32(nil, math.Float32bits(f))...)
}

// fake IED: accepts COTP, accepts the association (verifying the CONNECT is
// well-formed BER containing an MMS initiate request), then answers Reads
// from vals (item -> encoded MMS Data element). refuse: reject the association.
func fakeIED(t *testing.T, vals map[string][]byte, refuse bool) int {
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
				b, err := readTPKT(c)
				if err != nil || b[1] != 0xE0 {
					return
				}
				c.Write(tpkt([]byte{0x06, 0xD0, 0, 1, 0, 1, 0}))
				b, err = readTPKT(c)
				if err != nil {
					return
				}
				d, _ := cotpData(b)
				if d[0] != 0x0D {
					return
				}
				if _, ok := berFind(spduBody(d), 0xA8); !ok { // MMS initiate-Request present
					return
				}
				if refuse {
					c.Write(tpkt([]byte{0x02, 0xF0, 0x80, 0x0C, 0x00}))
					return
				}
				acc := tlv(0x61, tlv(0x30, tlv(0xA0, tlv(0x61, tlv(0xA2, tlv(0xAB, []byte{0x80, 1, 1}))))))
				c.Write(tpkt(append([]byte{0x02, 0xF0, 0x80, 0x0E, 0x00}, acc...)))
				for {
					b, err := readTPKT(c)
					if err != nil {
						return
					}
					d, _ := cotpData(b)
					pdu, ok := berFind(d[4:], 0xA0) // skip session header
					if !ok {
						return
					}
					// the first A0 found is the presentation wrapper; descend to the MMS PDU
					mms, _ := berFind(pdu, 0xA0)
					if mms == nil {
						mms = pdu
					}
					_, inv, rest, _ := berNext(mms)
					_, rd, _, _ := berNext(rest)
					lst, _ := berFind(rd, 0xA1)
					var results [][]byte
					for len(lst) > 0 {
						_, v, r, _ := berNext(lst)
						lst = r
						item, _ := berFind(v, 0x1A)
						_, _, rest2, _ := berNext(mustTLV(v))
						_ = rest2
						// second 0x1A is the item id
						_, dom, after, _ := berNext(inner1A(v))
						_, it, _, _ := berNext(after)
						_ = item
						key := string(dom) + "/" + string(it)
						if e, ok := vals[key]; ok {
							results = append(results, e)
						} else {
							results = append(results, []byte{0x80, 1, 10}) // object-non-existent
						}
					}
					resp := tlv(0xA1, []byte{0x02, 1, inv[0]}, tlv(0xA4, tlv(0xA1, results...)))
					c.Write(tpkt(append([]byte{0x02, 0xF0, 0x80, 0x01, 0x00, 0x01, 0x00}, tlv(0x61, tlv(0x30, []byte{0x02, 1, 3}, tlv(0xA0, resp)))...)))
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func mustTLV(b []byte) []byte { return b }

// inner1A returns the bytes starting at the domainId inside variable spec A0/A1.
func inner1A(v []byte) []byte {
	_, a0, _, _ := berNext(v)
	_, a1, _, _ := berNext(a0)
	return a1
}

func iedDev(port int) config.Device {
	return config.Device{ID: "ied", Profile: "iec61850", Host: "127.0.0.1", NetPort: port, Points: []config.Point{
		{ID: "kw", Key: "IED1LD0/MMXU1$MX$TotW$mag$f", Min: -1e6, Max: 1e6},
		{ID: "count", Key: "IED1LD0/CNT$ST$n", Min: 0, Max: 1e9},
		{ID: "closed", Key: "IED1LD0/XCBR1$ST$Pos", Min: 0, Max: 1},
	}}
}

var iedVals = map[string][]byte{
	"IED1LD0/MMXU1$MX$TotW$mag$f": f32(1234.5),
	"IED1LD0/CNT$ST$n":            {0x86, 2, 0x01, 0x00},
	"IED1LD0/XCBR1$ST$Pos":        {0x83, 1, 1},
}

func TestIEC61850Read(t *testing.T) {
	d, err := New(iedDev(fakeIED(t, iedVals, false)))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 2; i++ {
		rs, err := d.Poll(context.Background())
		if err != nil || len(rs) != 3 || rs[0].Value != 1234.5 || rs[1].Value != 256 || rs[2].Value != 1 {
			t.Fatalf("%v %v", rs, err)
		}
	}
}

func TestIEC61850Errors(t *testing.T) {
	d, _ := New(iedDev(fakeIED(t, iedVals, true)))
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("refused: %v", err)
	}
	missing := map[string][]byte{"IED1LD0/CNT$ST$n": {0x86, 1, 1}, "IED1LD0/XCBR1$ST$Pos": {0x83, 1, 0}}
	d, _ = New(iedDev(fakeIED(t, missing, false)))
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "access error") {
		t.Errorf("missing object: %v", err)
	}
	dev := iedDev(fakeIED(t, iedVals, false))
	dev.Points[0].Max = 10
	d, _ = New(dev)
	if _, err := d.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("range: %v", err)
	}
	for _, mut := range []func(*config.Device){
		func(d *config.Device) { d.Host = "" },
		func(d *config.Device) { d.Points[0].Key = "nodomain" },
		func(d *config.Device) { d.Points[1].Key = d.Points[0].Key },
		func(d *config.Device) { d.Points = nil },
	} {
		dev := iedDev(1)
		mut(&dev)
		if _, err := New(dev); err == nil {
			t.Error("invalid config accepted")
		}
	}
}

func TestBERRobust(t *testing.T) {
	for _, b := range [][]byte{nil, {0x30}, {0x30, 0x05, 1}, {0x30, 0x84, 1, 1, 1, 1}, {0x30, 0x80}} {
		if _, _, _, err := berNext(b); err == nil {
			t.Errorf("accepted %x", b)
		}
	}
	for _, v := range [][]byte{{}, {1, 2, 3}} {
		if _, err := decodeData(0x87, v); err == nil {
			t.Error("bad float accepted")
		}
	}
	if v, _ := decodeData(0x85, []byte{0xFF, 0xFE}); v != -2 {
		t.Errorf("signed int %v", v)
	}
}
