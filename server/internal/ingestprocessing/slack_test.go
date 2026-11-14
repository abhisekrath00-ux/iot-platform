package ingestprocessing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func localSlack(t *testing.T, h http.HandlerFunc) *slackSender {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	raw, err := NewSlackSender("synthetic-not-a-token")
	if err != nil {
		t.Fatal(err)
	}
	s := raw.(*slackSender)
	// Only tests can replace the official endpoint and trust the fixture CA.
	s.endpoint = srv.URL
	s.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	return s
}
func slackIntent() Intent {
	return Intent{ChannelType: "slack", Target: "C123ABC456", Message: "Synthetic reading42 https://example.invalid"}
}
func TestSlackOneAttemptAcceptanceAndLiteralPayload(t *testing.T) {
	var calls atomic.Int32
	s := localSlack(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-not-a-token" {
			t.Error("wrong method or credential")
		}
		var p map[string]any
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			t.Error("bad payload")
		}
		if len(p) != 7 || p["channel"] != slackIntent().Target || p["text"] != slackIntent().Message || p["parse"] != "none" || p["mrkdwn"] != false || p["link_names"] != false || p["unfurl_links"] != false || p["unfurl_media"] != false {
			t.Error("changed audience/text or enabled expansion", p)
		}
		io.WriteString(w, `{"ok":true,"channel":"C123ABC456","ts":"1503435956.000247","message":{"text":"Synthetic"}}`)
	})
	// Seven exact fields, not an extra hidden identity/recipient.
	if err := s.SendOnce(t.Context(), Envelope{}, slackIntent()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestSlackUncertainResponsesNeverRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"case-duplicate", `{"ok":false,"OK":true,"channel":"C123ABC456","ts":"1.000001"}`, 200},
		{"wrong-case", `{"OK":true,"channel":"C123ABC456","ts":"1.000001"}`, 200},
		{"duplicate-ok", `{"ok":false,"ok":true,"channel":"C123ABC456","ts":"1.000001"}`, 200},
		{"duplicate-nested-warning", `{"ok":true,"channel":"C123ABC456","ts":"1.000001","response_metadata":{"warnings":["truncated"],"warnings":[]}}`, 200},
		{"deep-json", `{"ok":true,"channel":"C123ABC456","ts":"1.000001","x":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}", 200},
		{"application-refusal", `{"ok":false,"error":"invalid_auth synthetic-not-a-token"}`, 200},
		{"missing-ok", `{"channel":"C123ABC456","ts":"1.000001"}`, 200},
		{"wrong-channel", `{"ok":true,"channel":"C999OTHER","ts":"1.000001"}`, 200},
		{"missing-timestamp", `{"ok":true,"channel":"C123ABC456"}`, 200},
		{"bad-timestamp", `{"ok":true,"channel":"C123ABC456","ts":"arbitrary"}`, 200},
		{"trailing-object", `{"ok":true,"channel":"C123ABC456","ts":"1.000001"}{}`, 200},
		{"warning", `{"ok":true,"channel":"C123ABC456","ts":"1.000001","warning":"message_truncated"}`, 200},
		{"metadata-warning", `{"ok":true,"channel":"C123ABC456","ts":"1.000001","response_metadata":{"warnings":["message_truncated"]}}`, 200},
		{"invalid-json", `not JSON`, 200},
		{"huge", strings.Repeat(" ", 65537), 200},
		{"rate-limit", `please retry`, 429},
		{"server-failure", `synthetic-not-a-token`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := localSlack(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			err := s.SendOnce(t.Context(), Envelope{}, slackIntent())
			if !errors.Is(err, ErrSlackOutcome) || calls.Load() != 1 || strings.Contains(err.Error(), "synthetic-not-a-token") {
				t.Fatal(err, calls.Load())
			}
		})
	}
}
func TestSlackRedirectDropCancellationAndInvalidInput(t *testing.T) {
	for _, mode := range []string{"redirect", "drop", "timeout", "cancel", "type", "name", "user", "empty", "long", "utf8", "mention", "channel-mention", "special-mention"} {
		t.Run(mode, func(t *testing.T) {
			var calls, elsewhere atomic.Int32
			other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { elsewhere.Add(1) }))
			defer other.Close()
			s := localSlack(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "redirect":
					w.Header().Set("Location", other.URL)
					w.WriteHeader(307)
				case "drop":
					c, _, _ := w.(http.Hijacker).Hijack()
					c.Close()
				default:
					select {
					case <-r.Context().Done():
					case <-time.After(500 * time.Millisecond):
					}
				}
			})
			ctx := t.Context()
			i := slackIntent()
			want := int32(1)
			if mode == "timeout" || mode == "cancel" {
				c, cn := context.WithTimeout(ctx, 50*time.Millisecond)
				defer cn()
				ctx = c
				if mode == "cancel" {
					cn()
					want = 0
				}
			}
			switch mode {
			case "type":
				i.ChannelType = "email"
				want = 0
			case "name":
				i.Target = "#general"
				want = 0
			case "user":
				i.Target = "U123ABC456"
				want = 0
			case "empty":
				i.Message = "  "
				want = 0
			case "long":
				i.Message = strings.Repeat("x", 4001)
				want = 0
			case "mention":
				i.Message = "<@U123ABC>"
				want = 0
			case "channel-mention":
				i.Message = "<#C123ABC>"
				want = 0
			case "special-mention":
				i.Message = "<!here>"
				want = 0
			case "utf8":
				i.Message = string([]byte{255})
				want = 0
			}
			if err := s.SendOnce(ctx, Envelope{}, i); err == nil {
				t.Fatal("accepted uncertain/invalid input")
			}
			if calls.Load() != want || elsewhere.Load() != 0 {
				t.Fatal(calls.Load(), elsewhere.Load(), want)
			}
		})
	}
	for _, token := range []string{"", "bad\r\nheader", "contains space", strings.Repeat("x", 4097)} {
		if _, err := NewSlackSender(token); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}
func TestSlackExecuteOnceRealPGQuarantinesProviderUncertainty(t *testing.T) {
	for _, mode := range []string{"accepted", "false", "drop", "redirect", "rate-limit", "revoke-between"} {
		t.Run(mode, func(t *testing.T) {
			x, e := fixture(t)
			exec(t, x.Pool, `UPDATE notification_channels SET type='slack',target='C123ABC456' WHERE id='c'`)
			mustAdmit(t, x, e)
			var calls atomic.Int32
			s := localSlack(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				// The real claim/history are committed before the first HTTP request.
				assertState(t, x, e, "executing")
				historyCount(t, x, 1, "processing_started")
				switch mode {
				case "accepted", "revoke-between":
					if mode == "revoke-between" {
						exec(t, x.Pool, `UPDATE notification_channels SET enabled=false WHERE id='c'`)
					}
					io.WriteString(w, `{"ok":true,"channel":"C123ABC456","ts":"1.000001"}`)
				case "false":
					io.WriteString(w, `{"ok":false,"error":"channel_not_found"}`)
				case "drop":
					c, _, _ := w.(http.Hijacker).Hijack()
					c.Close()
				case "redirect":
					w.Header().Set("Location", "https://example.invalid")
					w.WriteHeader(307)
				case "rate-limit":
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(429)
				}
			})
			err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s)
			want := int32(1)
			state := "outcome_unknown"
			if mode == "accepted" {
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
			if calls.Load() != want {
				t.Fatal(fmt.Sprint("provider attempts ", calls.Load(), " want ", want))
			}
		})
	}
}

func TestSlackTrustAndBodyDeadline(t *testing.T) {
	var calls atomic.Int32
	s := localSlack(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"ok":`)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	})
	s.client.Timeout = 50 * time.Millisecond
	if err := s.SendOnce(t.Context(), Envelope{}, slackIntent()); !errors.Is(err, ErrSlackOutcome) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
	// Removing the fixture CA must fail TLS before any request/credential reaches
	// the synthetic provider. The product constructor never skips verification.
	calls.Store(0)
	s.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = nil
	if err := s.SendOnce(t.Context(), Envelope{}, slackIntent()); !errors.Is(err, ErrSlackOutcome) || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
	raw, err := NewSlackSender("synthetic-not-a-token")
	if err != nil {
		t.Fatal(err)
	}
	prod := raw.(*slackSender)
	tr := prod.client.Transport.(*http.Transport)
	if prod.endpoint != "https://slack.com/api/chat.postMessage" || tr.Proxy != nil || !tr.DisableKeepAlives || tr.TLSClientConfig.InsecureSkipVerify || prod.client.Timeout != 10*time.Second {
		t.Fatal("unsafe constructor")
	}
}
