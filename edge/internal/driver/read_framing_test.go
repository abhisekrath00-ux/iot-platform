package driver

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

type fragmentedPort struct {
	fakePort
	chunk                  int
	response               []byte
	noProgress, shortWrite bool
	writesCount            int
}

func (p *fragmentedPort) Write(req []byte) (int, error) {
	p.writesCount++
	p.readBuf = bytes.NewReader(p.response)
	if p.shortWrite {
		return len(req) - 1, nil
	}
	return len(req), nil
}
func (p *fragmentedPort) Read(b []byte) (int, error) {
	if p.noProgress {
		return 0, nil
	}
	if p.chunk > 0 && len(b) > p.chunk {
		b = b[:p.chunk]
	}
	return p.readBuf.Read(b)
}
func rtuFrame(body []byte) []byte {
	out := append([]byte(nil), body...)
	c := crc16(out)
	return append(out, byte(c), byte(c>>8))
}
func TestRTUFragmentedRangesAndExceptionsOneRequest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fn, count int
		frame     []byte
		want      []uint16
		bad       string
	}{
		{"registers", 3, 3, rtuFrame([]byte{7, 3, 6, 0x42, 0xc8, 0, 0, 0, 42}), []uint16{17096, 0, 42}, ""},
		{"input registers", 4, 1, rtuFrame([]byte{7, 4, 2, 0x12, 0x34}), []uint16{4660}, ""},
		{"packed coils", 1, 10, rtuFrame([]byte{7, 1, 2, 0x81, 0x03}), []uint16{1, 0, 0, 0, 0, 0, 0, 1, 1, 1}, ""},
		{"packed discrete", 2, 3, rtuFrame([]byte{7, 2, 1, 5}), []uint16{1, 0, 1}, ""},
		{"exception", 3, 1, rtuFrame([]byte{7, 0x83, 2}), nil, "exception code 2"},
		{"wrong unit", 3, 1, rtuFrame([]byte{8, 3, 2, 0, 1}), nil, "address/function"},
		{"wrong function", 3, 1, rtuFrame([]byte{7, 4, 2, 0, 1}), nil, "address/function"},
		{"short declared count", 3, 2, rtuFrame([]byte{7, 3, 2, 0, 1}), nil, "byte count"},
		{"oversized declared count", 3, 1, rtuFrame([]byte{7, 3, 4, 0, 1, 0, 2}), nil, "byte count"},
		{"truncated body", 3, 2, []byte{7, 3, 4, 0}, nil, "EOF"},
		{"truncated header", 3, 1, []byte{7}, nil, "EOF"},
		{"bad crc", 3, 1, []byte{7, 3, 2, 0, 1, 0, 0}, nil, "crc"},
	} {
		for _, chunk := range []int{1, 2, 7} {
			t.Run(tc.name+string(rune('0'+chunk)), func(t *testing.T) {
				p := &fragmentedPort{chunk: chunk, response: tc.frame}
				g := newModbusGeneric(p, config.Device{ID: "synthetic", Address: 7})
				words, err := g.ReadRange(context.Background(), tc.fn, 100, tc.count)
				if p.writesCount != 1 {
					t.Fatal("request retried", p.writesCount)
				}
				if tc.bad != "" {
					if err == nil || !strings.Contains(err.Error(), tc.bad) || len(words) != 0 {
						t.Fatal(words, err)
					}
					return
				}
				if err != nil || len(words) != len(tc.want) {
					t.Fatal(words, err)
				}
				for i, w := range words {
					if w != tc.want[i] {
						t.Fatal(words, tc.want)
					}
				}
			})
		}
	}
}
func TestRTUNoProgressAndShortWriteFailWithoutRetry(t *testing.T) {
	for _, p := range []*fragmentedPort{{noProgress: true}, {shortWrite: true}} {
		g := newModbusGeneric(p, config.Device{Address: 7})
		started := time.Now()
		if _, err := g.ReadRange(context.Background(), 3, 0, 1); err == nil {
			t.Fatal("failed transaction accepted")
		}
		if time.Since(started) > time.Second || p.writesCount != 1 {
			t.Fatal("unbounded/no-progress read or retry", p.writesCount)
		}
	}
}
func TestTCPReadResponseCorrelationAndMalformedPDUs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func([]byte) []byte
		bad    string
	}{
		{"valid", func(b []byte) []byte { return b }, ""},
		{"protocol", func(b []byte) []byte { b[3] = 1; return b }, "protocol or unit"},
		{"unit", func(b []byte) []byte { b[6] = 8; return b }, "protocol or unit"},
		{"transaction", func(b []byte) []byte { b[1] ^= 1; return b }, "transaction"},
		{"function", func(b []byte) []byte { b[7] = 4; return b }, "malformed"},
		{"short PDU", func(b []byte) []byte { b[5] = 2; b[7] = 0x83; return b[:8] }, "truncated"},
		{"exception", func(b []byte) []byte { b[5] = 3; b[7] = 0x83; b[8] = 2; return b[:9] }, "exception code 2"},
		{"long exception", func(b []byte) []byte { b[7] = 0x83; return b }, "malformed Modbus exception"},
		{"wrong count", func(b []byte) []byte { b[5] = 5; b[8] = 2; return b[:11] }, "count does not match"},
		{"bad byte count", func(b []byte) []byte { b[8] = 3; return b }, "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := make([]byte, 12)
				if _, err := io.ReadFull(server, req); err != nil {
					return
				}
				body := []byte{req[0], req[1], 0, 0, 0, 7, 7, 3, 4, 0x42, 0xc8, 0, 0}
				body = tc.modify(body)
				server.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				for _, v := range body {
					if _, err := server.Write([]byte{v}); err != nil {
						return
					}
				}
			}()
			tcp := &tcpConn{conn: client, unit: 7}
			out, err := tcp.read(3, 0, 2)
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.bad) || len(out) != 0 {
					t.Fatal(out, err)
				}
			} else if err != nil || len(out) != 4 || binary.BigEndian.Uint16(out) != 17096 {
				t.Fatal(out, err)
			}
			client.Close()
			<-done
		})
	}
}
