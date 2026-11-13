package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
)

func readCommandPacket(c net.Conn) (byte, []byte, error) {
	var h [1]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return 0, nil, err
	}
	n, m := 0, 1
	for i := 0; i < 4; i++ {
		var b [1]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return 0, nil, err
		}
		n += int(b[0]&127) * m
		if b[0]&128 == 0 {
			if n > 65536 {
				return 0, nil, io.ErrShortBuffer
			}
			body := make([]byte, n)
			_, err := io.ReadFull(c, body)
			return h[0], body, err
		}
		m *= 128
	}
	return 0, nil, io.ErrUnexpectedEOF
}

// Real loopback sockets speak only the MQTT handshake and ACK used here. They
// are not real broker/actuator compatibility tests; payloads are synthetic.
func TestCommandTransportDeadlines(t *testing.T) {
	for _, stage := range []string{"connect-cancel", "publish-cancel", "publish-deadline", "accepted", "tls-accepted", "tls-wrong-host", "tls-no-client"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			cfg := map[string]string{"MQTT_HOST": "127.0.0.1", "MQTT_PORT": strconv.Itoa(l.Addr().(*net.TCPAddr).Port)}
			if strings.HasPrefix(stage, "tls-") {
				dir := t.TempDir()
				issue := pipelineTestCerts(t, dir)
				bc, bk := issue("broker", true)
				cc, ck := issue("api", false)
				cert, err := tls.LoadX509KeyPair(bc, bk)
				if err != nil {
					t.Fatal(err)
				}
				ca, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
				if err != nil {
					t.Fatal(err)
				}
				roots := x509.NewCertPool()
				if !roots.AppendCertsFromPEM(ca) {
					t.Fatal("CA")
				}
				l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12})
				cfg["MQTT_TLS"] = "true"
				cfg["MQTT_CA_FILE"] = filepath.Join(dir, "ca.crt")
				cfg["MQTT_CERT_FILE"] = cc
				cfg["MQTT_KEY_FILE"] = ck
			}
			reached := make(chan struct{})
			done := make(chan struct{})
			serverErr := make(chan error, 1)
			var publications atomic.Int32
			go func() {
				defer close(done)
				c, e := l.Accept()
				if e != nil {
					serverErr <- e
					return
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(4 * time.Second))
				h, _, e := readCommandPacket(c)
				if e != nil || h != 0x10 {
					if stage == "tls-wrong-host" || stage == "tls-no-client" {
						close(reached)
						return
					}
					serverErr <- io.ErrUnexpectedEOF
					return
				}
				if stage == "connect-cancel" {
					close(reached)
				} else {
					if _, e = c.Write([]byte{0x20, 2, 0, 0}); e != nil {
						serverErr <- e
						return
					}
				}
				for {
					h, body, e := readCommandPacket(c)
					if e != nil {
						return
					}
					if h>>4 == 3 {
						publications.Add(1)
						if h != 0x32 || len(body) < 4 {
							serverErr <- io.ErrUnexpectedEOF
							return
						}
						close(reached)
						if stage == "accepted" || stage == "tls-accepted" {
							n := int(binary.BigEndian.Uint16(body[:2]))
							if n+4 > len(body) {
								serverErr <- io.ErrUnexpectedEOF
								return
							}
							id := body[2+n : 4+n]
							_, e = c.Write([]byte{0x40, 2, id[0], id[1]})
							if e != nil {
								serverErr <- e
								return
							}
						}
					}
				}
			}()
			opts, err := mqttconfig.Options(func(k string) string { return cfg[k] }, "synthetic-command")
			if err != nil {
				t.Fatal(err)
			}
			if stage == "tls-wrong-host" {
				opts.TLSConfig.ServerName = "wrong.invalid"
			}
			if stage == "tls-no-client" {
				opts.TLSConfig.Certificates = nil
			}
			result := make(chan error, 1)
			limit := 2 * time.Second
			if stage == "publish-deadline" {
				limit = 250 * time.Millisecond
			}
			go func() {
				result <- publishCommandOnce(ctx, opts, "t/fixture/g/fixture/cmd", []byte(`{"synthetic":true}`), limit)
			}()
			select {
			case <-reached:
			case <-ctx.Done():
				t.Fatal("stage not reached")
			}
			if stage == "connect-cancel" || stage == "publish-cancel" {
				cancel()
			}
			var got error
			select {
			case got = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("caller did not return")
			}
			if opts.AutoReconnect || opts.ConnectRetry || !opts.CleanSession {
				t.Fatal("attempt permits background retry or persistent session")
			}
			success := stage == "accepted" || stage == "tls-accepted"
			if success != (got == nil) {
				t.Fatalf("result=%v", got)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("socket not closed")
			}
			select {
			case e := <-serverErr:
				t.Fatal(e)
			default:
			}
			want := int32(1)
			if stage == "connect-cancel" || stage == "tls-wrong-host" || stage == "tls-no-client" {
				want = 0
			}
			if publications.Load() != want {
				t.Fatalf("publish packets=%d want%d", publications.Load(), want)
			}
		})
	}
}

// The production dispatchCore, real SQL claim and real socket cancellation
// compose here. The listener cannot actuate and intentionally withholds PUBACK.
func TestIntegrationCommandTransportUnknown(t *testing.T) {
	s, _ := testServer(t)
	const tenant = "itest-command-transport"
	seed(t, s, tenant)
	ctx := t.Context()
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,status,requested_by,approved_by,issued_at,expires_at)VALUES('transport-unknown',$1,'fixture-gw','fixture-device','synthetic.noop','approved','test-user','test-user',now(),now()+interval '1 minute')`, tenant); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for k, v := range map[string]string{"MQTT_HOST": "127.0.0.1", "MQTT_PORT": strconv.Itoa(l.Addr().(*net.TCPAddr).Port), "MQTT_TLS": "false", "MQTT_CA_FILE": "", "MQTT_CERT_FILE": "", "MQTT_KEY_FILE": ""} {
		t.Setenv(k, v)
	}
	published := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		h, _, e := readCommandPacket(c)
		if e != nil || h != 0x10 {
			return
		}
		if _, e = c.Write([]byte{0x20, 2, 0, 0}); e != nil {
			return
		}
		h, _, e = readCommandPacket(c)
		if e != nil || h != 0x32 {
			return
		}
		close(published)
		_, _, _ = readCommandPacket(c)
	}()
	attempt, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan string, 1)
	go func() { next, _ := s.dispatchCore(attempt, tenant, "transport-unknown"); result <- next }()
	select {
	case <-published:
	case <-time.After(3 * time.Second):
		t.Fatal("production dispatch did not publish")
	}
	cancel()
	select {
	case next := <-result:
		if next != "outcome_unknown" {
			t.Fatalf("result %s", next)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled caller hung")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled socket survived")
	}
	var status string
	if err = s.st.Pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id='transport-unknown' AND tenant_id=$1`, tenant).Scan(&status); err != nil || status != "outcome_unknown" {
		t.Fatalf("persisted %s / %v", status, err)
	}
	// Already claimed: it cannot create a new transport even while listener exists.
	next, _ := s.dispatchCore(ctx, tenant, "transport-unknown")
	if next != "not_dispatched" {
		t.Fatalf("unknown claim reentered: %s", next)
	}
}
