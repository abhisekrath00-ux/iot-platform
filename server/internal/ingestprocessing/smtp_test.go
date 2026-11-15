package ingestprocessing

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type smtpLab struct {
	connections, auth, mail, rcpt, data atomic.Int32
	mu                                  sync.Mutex
	message, recipient                  string
	beforeData                          func()
}

func smtpFixture(t *testing.T, mode, behavior string) (*smtpSender, *smtpLab) {
	t.Helper()
	seed := httptest.NewTLSServer(nil)
	cert := seed.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(seed.Certificate())
	seed.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lab := &smtpLab{}
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			lab.connections.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				var wire net.Conn = conn
				tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
				if mode == "implicit" {
					tc := tls.Server(wire, tlsConfig)
					if tc.Handshake() != nil {
						return
					}
					wire = tc
				}
				rd := bufio.NewReader(wire)
				reply := func(s string) { fmt.Fprint(wire, s+"\r\n") }
				if behavior == "greeting-stall" {
					time.Sleep(200 * time.Millisecond)
					return
				}
				if behavior == "greeting-flood" {
					fmt.Fprint(wire, "220-"+strings.Repeat("x", 70000)+"\r\n220 done\r\n")
					return
				}
				reply("220 synthetic.invalid ESMTP")
				encrypted := mode == "implicit"
				for {
					line, err := rd.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
					command := strings.SplitN(line, " ", 2)[0]
					switch command {
					case "EHLO", "HELO":
						reply("250-synthetic.invalid")
						if !encrypted && behavior != "no-starttls" {
							reply("250-STARTTLS")
						}
						if behavior == "no-auth-extension" {
							reply("250 SIZE 1000000")
						} else {
							reply("250 AUTH PLAIN")
						}
					case "STARTTLS":
						reply("220 begin TLS")
						tc := tls.Server(wire, tlsConfig)
						if tc.Handshake() != nil {
							return
						}
						wire = tc
						rd = bufio.NewReader(wire)
						encrypted = true
					case "AUTH":
						lab.auth.Add(1)
						if !encrypted {
							t.Error("credential sent before TLS")
						}
						parts := strings.SplitN(line, " ", 3)
						if len(parts) != 3 {
							t.Error("wrong AUTH")
						} else {
							v, err := base64.StdEncoding.DecodeString(parts[2])
							if err != nil || string(v) != "\x00synthetic-user\x00synthetic-password" {
								t.Error("wrong synthetic credential")
							}
						}
						if behavior == "no-auth-extension" {
							t.Error("unexpected AUTH without extension")
						}
						if behavior == "auth-refusal" {
							reply("535 synthetic credential refusal")
						} else {
							reply("235 authenticated")
						}
					case "MAIL":
						lab.mail.Add(1)
						if !encrypted {
							t.Error("MAIL before TLS")
						}
						if !strings.HasPrefix(line, "MAIL FROM:<sender@example.invalid>") {
							t.Error("changed sender", line)
						}
						if behavior == "mail-refusal" {
							reply("550 no")
						} else {
							reply("250 sender")
						}
					case "RCPT":
						lab.rcpt.Add(1)
						lab.mu.Lock()
						lab.recipient = line
						lab.mu.Unlock()
						if behavior == "rcpt-refusal" {
							reply("550 no")
						} else {
							reply("250 recipient")
						}
					case "DATA":
						if lab.beforeData != nil {
							lab.beforeData()
						}
						if behavior == "data-refusal" {
							reply("554 no")
							continue
						}
						lab.data.Add(1)
						reply("354 body")
						var text strings.Builder
						for {
							v, err := rd.ReadString('\n')
							if err != nil {
								return
							}
							if v == ".\r\n" {
								break
							}
							if strings.HasPrefix(v, "..") {
								v = v[1:]
							}
							text.WriteString(v)
						}
						lab.mu.Lock()
						lab.message = text.String()
						lab.mu.Unlock()
						switch behavior {
						case "drop-after-body":
							return
						case "final-stall":
							time.Sleep(200 * time.Millisecond)
							return
						case "final-refusal":
							reply("451 uncertain relay state")
						default:
							reply("250 queued synthetic")
						}
					case "*":
						reply("501 authentication canceled")
					case "QUIT":
						reply("221 goodbye")
						return
					default:
						t.Error("unexpected SMTP command", command)
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ln.Close(); <-done; wg.Wait() })
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	pn, _ := strconv.Atoi(port)
	raw, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: pn, Mode: mode, From: "sender@example.invalid", Username: "synthetic-user", Password: "synthetic-password"})
	if err != nil {
		t.Fatal(err)
	}
	sender := raw.(*smtpSender)
	sender.tls.RootCAs = roots
	return sender, lab
}
func smtpIntent() Intent {
	return Intent{ChannelType: "email", Target: "recipient@example.invalid", Message: "First line\n.\nTo: not-a-header@example.invalid\nUnicode: 温度42"}
}
func TestSMTPVerifiedTLSOneAttempt(t *testing.T) {
	for _, mode := range []string{"implicit", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			s, lab := smtpFixture(t, mode, "accepted")
			if err := s.SendOnce(t.Context(), Envelope{}, smtpIntent()); err != nil {
				t.Fatal(err)
			}
			if lab.connections.Load() != 1 || lab.auth.Load() != 1 || lab.mail.Load() != 1 || lab.rcpt.Load() != 1 || lab.data.Load() != 1 {
				t.Fatal("not one exact SMTP transaction")
			}
			lab.mu.Lock()
			defer lab.mu.Unlock()
			if lab.recipient != "RCPT TO:<recipient@example.invalid>" {
				t.Fatal(lab.recipient)
			}
			msg, err := mail.ReadMessage(strings.NewReader(lab.message))
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
			if err != nil {
				t.Fatal(err)
			}
			normalized := strings.ReplaceAll(string(b), "\r\n", "\n")
			if strings.TrimSuffix(normalized, "\n") != smtpIntent().Message || msg.Header.Get("To") != smtpIntent().Target || msg.Header.Get("Bcc") != "" || len(msg.Header) != 7 {
				t.Fatal(msg.Header, string(b))
			}
		})
	}
}
func TestSMTPNoRetryOnRefusalOrLostResponse(t *testing.T) {
	for _, behavior := range []string{"auth-refusal", "no-auth-extension", "mail-refusal", "rcpt-refusal", "data-refusal", "final-refusal", "drop-after-body", "greeting-stall", "greeting-flood", "final-stall", "no-starttls"} {
		t.Run(behavior, func(t *testing.T) {
			s, lab := smtpFixture(t, "starttls", behavior)
			ctx := t.Context()
			if strings.Contains(behavior, "stall") {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
				defer cancel()
			}
			err := s.SendOnce(ctx, Envelope{}, smtpIntent())
			if !errors.Is(err, ErrSMTPOutcome) || lab.connections.Load() != 1 || lab.data.Load() > 1 || strings.Contains(err.Error(), "synthetic-password") {
				t.Fatal(err, lab.connections.Load(), lab.data.Load())
			}
			if behavior == "no-starttls" && (lab.auth.Load() != 0 || lab.mail.Load() != 0) {
				t.Fatal("plaintext downgrade")
			}
		})
	}
}
func TestSMTPInputTrustAndCancellation(t *testing.T) {
	s, lab := smtpFixture(t, "implicit", "accepted")
	for _, target := range []string{"display <a@example.invalid>", "a@example.invalid,b@example.invalid", "a@example.invalid\r\nBcc:other@example.invalid", " 温度@example.invalid", "", "local"} {
		i := smtpIntent()
		i.Target = target
		if !errors.Is(s.SendOnce(t.Context(), Envelope{}, i), ErrSMTPInput) {
			t.Fatal("accepted recipient", target)
		}
	}
	for _, body := range []string{"", strings.Repeat("x", 65537), string([]byte{255}), "bad\x00text"} {
		i := smtpIntent()
		i.Message = body
		if !errors.Is(s.SendOnce(t.Context(), Envelope{}, i), ErrSMTPInput) {
			t.Fatal("bad text")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(s.SendOnce(ctx, Envelope{}, smtpIntent()), ErrSMTPOutcome) || lab.connections.Load() != 0 {
		t.Fatal("canceled request connected")
	}
	s.tls.RootCAs = nil
	if !errors.Is(s.SendOnce(t.Context(), Envelope{}, smtpIntent()), ErrSMTPOutcome) || lab.auth.Load() != 0 || lab.mail.Load() != 0 {
		t.Fatal("untrusted peer received AUTH or MAIL")
	}
	s2, lab2 := smtpFixture(t, "starttls", "accepted")
	s2.tls.ServerName = "wrong.example.invalid"
	if !errors.Is(s2.SendOnce(t.Context(), Envelope{}, smtpIntent()), ErrSMTPOutcome) || lab2.auth.Load() != 0 || lab2.mail.Load() != 0 {
		t.Fatal("wrong hostname received AUTH or MAIL")
	}
	for _, c := range []SMTPConfig{
		{Host: "host", Port: 587, Mode: "plain", From: "a@example.invalid"},
		{Host: "bad/host", Port: 587, Mode: "starttls", From: "a@example.invalid"},
		{Host: "host", Port: 0, Mode: "starttls", From: "a@example.invalid"},
		{Host: "host", Port: 587, Mode: "starttls", From: "A <a@example.invalid>"},
		{Host: "host", Port: 587, Mode: "starttls", From: "a@example.invalid", Username: "user"},
	} {
		if _, err := NewSMTPSender(c); !errors.Is(err, ErrSMTPInput) {
			t.Fatal("bad config accepted")
		}
	}
}
func TestSMTPExecuteOncePGNoRearm(t *testing.T) {
	for _, behavior := range []string{"accepted", "drop-after-body", "final-refusal", "rcpt-refusal", "revoke-between"} {
		t.Run(behavior, func(t *testing.T) {
			x, e := fixture(t)
			mustAdmit(t, x, e)
			s, lab := smtpFixture(t, "implicit", behavior)
			lab.beforeData = func() {
				assertState(t, x, e, "executing")
				historyCount(t, x, 1, "processing_started")
				if behavior == "revoke-between" {
					exec(t, x.Pool, `UPDATE notification_channels SET enabled=false WHERE id='c'`)
				}
			}
			err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s)
			want := int32(1)
			state := "outcome_unknown"
			if behavior == "accepted" {
				want = 2
				state = "completed"
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrUnknown) {
				t.Fatal(err)
			}
			assertState(t, x, e, state)
			if err = x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrState) {
				t.Fatal(err)
			}
			if lab.connections.Load() != want {
				t.Fatal("rearmed or repeated", lab.connections.Load(), want)
			}
		})
	}
}

func TestSMTPExplicitTLSRelayWithoutAuth(t *testing.T) {
	s, lab := smtpFixture(t, "starttls", "accepted")
	s.config.Username = ""
	s.config.Password = ""
	if err := s.SendOnce(t.Context(), Envelope{}, smtpIntent()); err != nil || lab.auth.Load() != 0 || lab.data.Load() != 1 {
		t.Fatal(err, lab.auth.Load(), lab.data.Load())
	}
}
