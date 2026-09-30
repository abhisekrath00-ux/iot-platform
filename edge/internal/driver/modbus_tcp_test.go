package driver

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// fakeModbusTCP answers fn3/fn4 reads with register value = 100+reg.
func fakeModbusTCP(t *testing.T) (host string, port int) {
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
					req := make([]byte, 12)
					if _, err := io.ReadFull(c, req); err != nil {
						return
					}
					fn := req[7]
					reg := binary.BigEndian.Uint16(req[8:])
					cnt := binary.BigEndian.Uint16(req[10:])
					data := make([]byte, 0, cnt*2)
					for i := uint16(0); i < cnt; i++ {
						data = binary.BigEndian.AppendUint16(data, 100+reg+i)
					}
					resp := []byte{req[0], req[1], 0, 0, 0, byte(3 + len(data)), req[6], fn, byte(len(data))}
					c.Write(append(resp, data...))
				}
			}(c)
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func TestModbusTCPPoll(t *testing.T) {
	host, port := fakeModbusTCP(t)
	d, err := NewWithOpener(nil, config.Device{
		ID: "plc1", Profile: "modbus-tcp", Host: host, NetPort: port, Address: 1, Interval: time.Second,
		Points: []config.Point{
			{ID: "temp", Register: 10, Func: 3, Type: "u16", Scale: 0.1, Min: 0, Max: 100},
			{ID: "big", Register: 0, Func: 4, Type: "u32", Min: 0, Max: 1e9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r, err := d.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 || r[0].Value < 10.99 || r[0].Value > 11.01 {
		t.Fatalf("unexpected readings %+v", r)
	}
	if r[1].Value != float64(100)*65536+101 {
		t.Fatalf("u32 decode wrong: %v", r[1].Value)
	}
}

func TestModbusTCPReconnect(t *testing.T) {
	d, _ := NewWithOpener(nil, config.Device{ID: "x", Profile: "modbus-tcp", Host: "127.0.0.1", NetPort: 1, Address: 1,
		Points: []config.Point{{ID: "a", Register: 0, Min: 0, Max: 1}}})
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("expected dial error")
	}
}
