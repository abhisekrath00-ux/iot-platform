package ingestprocessing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func mixedFixture(t *testing.T) (Engine, Envelope) {
	x, e := fixture(t)
	exec(t, x.Pool, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('c2','a','slack','C123ABC456',true);
 UPDATE flow_versions SET definition='{"trigger":{"device_id":"d","point_id":"p","op":">","value":1},"steps":[{"type":"notify","channel_id":"c","message":"first"},{"type":"notify","channel_id":"c2","message":"second"},{"type":"notify","channel_id":"c","message":"third"}]}' WHERE id='v';
 UPDATE devices SET lat_point='p',lon_point='lon',lat=1,lon=2 WHERE id='d'`)
	return x, e
}
func TestBatchSenderMixedTLSProtocolsPG(t *testing.T) {
	for _, mode := range []string{"accepted", "smtp-uncertain", "slack-uncertain", "revoke-after-smtp", "revoke-after-slack"} {
		t.Run(mode, func(t *testing.T) {
			x, e := mixedFixture(t)
			mustAdmit(t, x, e)
			smtpBehavior := "accepted"
			if mode == "smtp-uncertain" {
				smtpBehavior = "drop-after-body"
			}
			sm, lab := smtpFixture(t, "starttls", smtpBehavior)
			var slackCalls atomic.Int32
			sl := localSlack(t, func(w http.ResponseWriter, r *http.Request) {
				slackCalls.Add(1)
				assertState(t, x, e, "executing")
				historyCount(t, x, 1, "processing_started")
				if lab.data.Load() != 1 {
					t.Error("retained order changed")
				}
				if mode == "revoke-after-slack" {
					exec(t, x.Pool, `UPDATE notification_channels SET enabled=false WHERE id='c'`)
				}
				if mode == "slack-uncertain" {
					io.WriteString(w, `{"ok":false,"error":"synthetic refusal"}`)
				} else {
					io.WriteString(w, `{"ok":true,"channel":"C123ABC456","ts":"1.000001"}`)
				}
			})
			lab.beforeData = func() {
				assertState(t, x, e, "executing")
				historyCount(t, x, 1, "processing_started")
				// Transactional position metadata must be committed before the first send.
				_, _, latFix, _ := readPosition(t, x)
				if latFix == nil || *latFix != 42 {
					t.Error("position not committed before I/O")
				}
				if mode == "revoke-after-smtp" {
					exec(t, x.Pool, `UPDATE notification_channels SET enabled=false WHERE id='c2'`)
				}
			}
			sender := &notificationSender{smtp: sm, slack: sl}
			err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender)
			state := "outcome_unknown"
			wantSMTP, wantSlack := int32(1), int32(1)
			switch mode {
			case "accepted":
				state = "completed"
				wantSMTP = 2
				if err != nil {
					t.Fatal(err)
				}
			case "smtp-uncertain", "revoke-after-smtp":
				wantSlack = 0
			}
			if mode != "accepted" && !errors.Is(err, ErrUnknown) {
				t.Fatal(err)
			}
			assertState(t, x, e, state)
			if lab.connections.Load() != wantSMTP || slackCalls.Load() != wantSlack {
				t.Fatal(lab.connections.Load(), slackCalls.Load(), wantSMTP, wantSlack)
			}
			if err = x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); !errors.Is(err, ErrState) {
				t.Fatal(err)
			}
			if lab.connections.Load() != wantSMTP || slackCalls.Load() != wantSlack {
				t.Fatal("rearmed batch")
			}
		})
	}
}
func TestBatchSenderPreflightBeforeAnyInternalOrExternalWork(t *testing.T) {
	for _, mode := range []string{"missing-slack", "missing-smtp", "invalid-later-email", "invalid-later-slack", "typed-nil-router", "typed-nil-smtp", "typed-nil-slack", "direct-email-mixed", "direct-slack-mixed"} {
		t.Run(mode, func(t *testing.T) {
			x, e := mixedFixture(t)
			switch mode {
			case "invalid-later-email":
				exec(t, x.Pool, `UPDATE notification_channels SET target='Display <bad@example.invalid>' WHERE id='c2';UPDATE notification_channels SET type='email' WHERE id='c2'`)
			case "invalid-later-slack":
				exec(t, x.Pool, `UPDATE notification_channels SET target='U123ABC456' WHERE id='c2'`)
			}
			mustAdmit(t, x, e)
			sm, lab := smtpFixture(t, "implicit", "accepted")
			var slackCalls atomic.Int32
			sl := localSlack(t, func(w http.ResponseWriter, r *http.Request) { slackCalls.Add(1) })
			router := &notificationSender{smtp: sm, slack: sl}
			var sender Sender = router
			switch mode {
			case "missing-slack":
				router.slack = nil
			case "missing-smtp":
				router.smtp = nil
			case "typed-nil-router":
				sender = (*notificationSender)(nil)
			case "typed-nil-smtp":
				sender = (*smtpSender)(nil)
			case "typed-nil-slack":
				sender = (*slackSender)(nil)
			case "direct-email-mixed":
				sender = sm
			case "direct-slack-mixed":
				sender = sl
			}
			if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); err == nil || errors.Is(err, ErrUnknown) {
				t.Fatal("invalid batch claimed", err)
			}
			assertState(t, x, e, "pending")
			historyCount(t, x, 0, "processing_started")
			_, _, latFix, _ := readPosition(t, x)
			if latFix != nil {
				t.Fatal("preflight changed position")
			}
			if lab.connections.Load() != 0 || slackCalls.Load() != 0 {
				t.Fatal("preflight performed provider I/O")
			}
		})
	}
}
func TestBatchSenderConstructorAndPureValidation(t *testing.T) {
	if _, err := NewNotificationSender(nil, ""); !errors.Is(err, ErrSenderUnavailable) {
		t.Fatal(err)
	}
	cfg := SMTPConfig{Host: "mail.example.invalid", Port: 587, Mode: "starttls", From: "sender@example.invalid"}
	raw, err := NewNotificationSender(&cfg, "synthetic-not-a-token")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Host = "changed.invalid"
	s := raw.(*notificationSender)
	if s.smtp.config.Host != "mail.example.invalid" {
		t.Fatal("constructor retained mutable config")
	}
	good := []Intent{smtpIntent(), slackIntent()}
	if err = s.validateIntents(good); err != nil {
		t.Fatal(err)
	}
	// No tenant/provider lookup or dialing is needed for this pure preflight.
	for _, kind := range []string{"http", "sms", "telegram", "whatsapp", "", "SLACK"} {
		bad := append([]Intent{}, good...)
		bad = append(bad, Intent{ChannelType: kind, Target: "https://example.invalid"})
		if !errors.Is(s.validateIntents(bad), ErrSenderUnavailable) {
			t.Fatal("unknown channel dispatched", kind)
		}
	}
	onlySMTP, err := NewNotificationSender(&cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(onlySMTP.(intentPreflight).validateIntents(good), ErrSenderUnavailable) {
		t.Fatal("missing adapter accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = s.SendOnce(ctx, Envelope{}, slackIntent()); err == nil {
		t.Fatal("canceled request accepted")
	}
	// Bounded input errors are generic, not a disclosure of recipient or message.
	bad := slackIntent()
	bad.Target = "invalid-secret-recipient"
	if err = s.validateIntents([]Intent{bad}); err == nil || strings.Contains(err.Error(), bad.Target) {
		t.Fatal(err)
	}
}
func TestBatchSenderPreflightReleasesTransaction(t *testing.T) {
	x, e := mixedFixture(t)
	mustAdmit(t, x, e)
	// A refused missing-adapter batch must release its row lock and connection.
	sender := &notificationSender{}
	for range 12 {
		if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); !errors.Is(err, ErrSenderUnavailable) {
			t.Fatal(err)
		}
	}
	tx, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `SELECT 1 FROM ingest_processing_v1 WHERE event_id=$1 FOR UPDATE NOWAIT`, e.EventID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM ingest_processing_v1 WHERE event_id=$1 AND state='pending' AND attempt_id IS NULL`, e.EventID).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
