package discover

import (
	"context"
	"errors"
	"github.com/gosnmp/gosnmp"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestScanSNMPGuards(t *testing.T) {
	if _, err := ScanSNMP(context.Background(), "8.8.8.0/24", "public", time.Second); err == nil {
		t.Error("public range accepted")
	}
	if _, err := ScanSNMP(context.Background(), "192.168.1.0/24", "", time.Second); err == nil {
		t.Error("empty community accepted")
	}
	if got := clip("a\x00b\nc"); got != "a b c" {
		t.Errorf("clip %q", got)
	}
}

// A wire-level simulated agent, not a stubbed result. Response order is
// deliberately reversed and unknown OIDs cannot masquerade as sysName.
func TestSNMPProbeWire(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	var requests atomic.Int32
	go func() {
		buf := make([]byte, 4096)
		dec := &gosnmp.GoSNMP{}
		for {
			n, from, e := pc.ReadFrom(buf)
			if e != nil {
				return
			}
			req, e := dec.SnmpDecodePacket(buf[:n])
			if e != nil {
				continue
			}
			requests.Add(1)
			if req.Community != "test-only" {
				continue
			}
			if req.PDUType != gosnmp.GetRequest {
				continue
			}
			resp := &gosnmp.SnmpPacket{Version: gosnmp.Version2c, Community: req.Community, RequestID: req.RequestID, PDUType: gosnmp.GetResponse, Variables: []gosnmp.SnmpPDU{
				{Name: sysNameOID, Type: gosnmp.OctetString, Value: []byte("UPS\x00one")},
				{Name: ".1.3.6.1.4.1.999.0", Type: gosnmp.OctetString, Value: []byte("not a description")},
				{Name: sysDescrOID, Type: gosnmp.OctetString, Value: []byte("Simulated UPS")}}}
			b, e := resp.MarshalMsg()
			if e == nil {
				pc.WriteTo(b, from)
			}
		}
	}()
	port := uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	hs, err := scanSNMPHosts(context.Background(), []string{"127.0.0.1"}, "test-only", port, 100*time.Millisecond)
	if err != nil || len(hs) != 1 || hs[0].Name != "UPS one" || hs[0].Descr != "Simulated UPS" {
		t.Fatalf("%+v %v", hs, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests %d", requests.Load())
	}
	if hit, ok := probeSNMP(context.Background(), "127.0.0.1", "wrong", port, 50*time.Millisecond); ok {
		t.Fatalf("wrong community %+v", hit)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	start := time.Now()
	_, err = scanSNMPHosts(ctx, []string{"127.0.0.1", "127.0.0.1"}, "wrong", port, 5*time.Second)
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("cancel %v elapsed %v", err, time.Since(start))
	}
}
func TestSNMPTimeoutAndTextGuards(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second, 6 * time.Second} {
		if _, err := ScanSNMP(context.Background(), "192.168.1.0/30", "test-only", d); err == nil {
			t.Fatal("bad timeout")
		}
	}
	if s := clip(strings.Repeat("界", 41)); !utf8.ValidString(s) || len(s) > 120 {
		t.Fatalf("invalid clipped UTF8 %q", s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanSNMP(ctx, "192.168.1.0/30", "test-only", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSNMPProbeRejectsErrorAndWrongObjects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status gosnmp.SNMPError
		vars   []gosnmp.SnmpPDU
		want   bool
	}{
		{"agent error", gosnmp.GenErr, []gosnmp.SnmpPDU{{Name: sysNameOID, Type: gosnmp.OctetString, Value: []byte("untrusted result")}}, false},
		{"unknown object", gosnmp.NoError, []gosnmp.SnmpPDU{{Name: ".1.3.6.1.4.1.999.0", Type: gosnmp.OctetString, Value: []byte("unknown")}}, false},
		{"missing object", gosnmp.NoError, []gosnmp.SnmpPDU{{Name: sysNameOID, Type: gosnmp.NoSuchObject}}, false},
		{"numeric instead of text", gosnmp.NoError, []gosnmp.SnmpPDU{{Name: sysNameOID, Type: gosnmp.Integer, Value: 1}}, false},
		{"partial description", gosnmp.NoError, []gosnmp.SnmpPDU{{Name: sysDescrOID, Type: gosnmp.OctetString, Value: []byte("description only")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pc, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer pc.Close()
			go func() {
				buf := make([]byte, 4096)
				n, from, e := pc.ReadFrom(buf)
				if e != nil {
					return
				}
				req, e := (&gosnmp.GoSNMP{}).SnmpDecodePacket(buf[:n])
				if e != nil {
					return
				}
				resp := &gosnmp.SnmpPacket{Version: gosnmp.Version2c, Community: req.Community, RequestID: req.RequestID, PDUType: gosnmp.GetResponse, Error: tc.status, Variables: tc.vars}
				b, e := resp.MarshalMsg()
				if e == nil {
					pc.WriteTo(b, from)
				}
			}()
			_, ok := probeSNMP(context.Background(), "127.0.0.1", "test-only", uint16(pc.LocalAddr().(*net.UDPAddr).Port), 100*time.Millisecond)
			if ok != tc.want {
				t.Fatalf("got %v", ok)
			}
		})
	}
}
