package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
)

// Deterministic in-memory protocol peers complement, not replace, real mTLS.
func TestApplyOnceCandidateProtocol(t *testing.T) {
	for _, mode := range []string{"ack", "ack-withheld-timeout", "cancel-after-delivery", "cancel-before-connack", "rejected-connack"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var dials, pubs atomic.Int32
			closed := make(chan struct{})
			opts := mqtt.NewClientOptions().AddBroker("tcp://synthetic.invalid:1883").SetClientID("isolated-apply-peer")
			opts.SetCustomOpenConnectionFn(func(_ *url.URL, o mqtt.ClientOptions) (net.Conn, error) {
				dials.Add(1)
				a, b := net.Pipe()
				go func() {
					defer close(closed)
					defer b.Close()
					if _, err := packets.ReadPacket(b); err != nil {
						return
					}
					if mode == "cancel-before-connack" {
						cancel()
						b.SetDeadline(time.Now().Add(o.ConnectTimeout))
						packets.ReadPacket(b)
						return
					}
					ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
					if mode == "rejected-connack" {
						ack.ReturnCode = packets.ErrRefusedNotAuthorised
						ack.Write(b)
						return
					}
					if ack.Write(b) != nil {
						return
					}
					for {
						p, err := packets.ReadPacket(b)
						if err != nil {
							return
						}
						pub, ok := p.(*packets.PublishPacket)
						if !ok {
							continue
						}
						pubs.Add(1)
						if pub.Qos != 1 || pub.Retain || pub.Dup || string(pub.Payload) != "synthetic-only" {
							t.Error("unexpected publication envelope")
						}
						if mode == "cancel-after-delivery" {
							cancel()
						}
						if mode == "ack" {
							reply := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
							reply.MessageID = pub.MessageID
							reply.Write(b)
						}
					}
				}()
				return a, nil
			})
			begin := time.Now()
			err := publishApplyOneConnection(ctx, opts, "synthetic/config", []byte("synthetic-only"), 80*time.Millisecond)
			if (mode == "ack") != (err == nil) {
				t.Fatal("publication outcome", err)
			}
			if time.Since(begin) > time.Second {
				t.Fatal("bounded local peer exceeded guard")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("socket still alive after return")
			}
			expected := int32(1)
			if mode == "cancel-before-connack" || mode == "rejected-connack" {
				expected = 0
			}
			if dials.Load() != 1 || pubs.Load() != expected || !opts.AutoReconnect || opts.ConnectRetry || opts.ConnectTimeout != 30*time.Second {
				t.Fatal("attempts or shared options changed", dials.Load(), pubs.Load())
			}
		})
	}
}

func TestApplyOnceCandidateRejectsBeforeConnect(t *testing.T) {
	var calls atomic.Int32
	opts := mqtt.NewClientOptions().AddBroker("tcp://synthetic.invalid:1883")
	opts.SetCustomOpenConnectionFn(func(_ *url.URL, _ mqtt.ClientOptions) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("must not dial")
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(publishApplyOneConnection(ctx, opts, "t", nil, time.Second), context.Canceled) {
		t.Fatal("missing cancellation")
	}
	for _, invalid := range []*mqtt.ClientOptions{nil, &mqtt.ClientOptions{Servers: []*url.URL{nil}}, mqtt.NewClientOptions(), mqtt.NewClientOptions().AddBroker("tcp://a:1").AddBroker("tcp://b:2")} {
		if publishApplyOneConnection(t.Context(), invalid, "t", nil, time.Second) == nil {
			t.Fatal("invalid options accepted")
		}
	}
	for _, timeout := range []time.Duration{0, -1, 11 * time.Second} {
		if publishApplyOneConnection(t.Context(), opts, "t", nil, timeout) == nil {
			t.Fatal("bad timeout")
		}
	}
	if publishApplyOneConnection(t.Context(), opts, "", nil, time.Second) == nil || calls.Load() != 0 {
		t.Fatal("invalid/pre-cancelled attempted connection")
	}
}

func TestApplyOnceDirectTLSHandshakeTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 4096)
		for {
			if _, e = c.Read(buf); e != nil {
				return
			}
		}
	}()
	u := &url.URL{Scheme: "ssl", Host: l.Addr().String()}
	begin := time.Now()
	if _, err = openApplyOnceDirect(t.Context(), u, &tls.Config{MinVersion: tls.VersionTLS12}, 50*time.Millisecond); err == nil {
		t.Fatal("stalled handshake accepted")
	}
	if time.Since(begin) > time.Second {
		t.Fatal("TLS handshake unbounded")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed-out TLS socket leaked")
	}
	for _, u := range []*url.URL{nil, {Scheme: "wss", Host: "unsupported.invalid"}} {
		if _, e := openApplyOnceDirect(t.Context(), u, nil, time.Second); e == nil {
			t.Fatal("unsupported transport")
		}
	}
}
