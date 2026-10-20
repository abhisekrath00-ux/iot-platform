package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
)

// fakeTg implements the replier + telegram interfaces the inbound poller uses.
type fakeTg struct {
	mu      sync.Mutex
	updates []notify.TelegramUpdate
	sent    []struct{ chat, text string }
}

func (f *fakeTg) Email(context.Context, []string, string, string) error { return nil }
func (f *fakeTg) Slack(context.Context, string, string) error           { return nil }
func (f *fakeTg) Telegram(_ context.Context, chat, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, struct{ chat, text string }{chat, text})
	return nil
}
func (f *fakeTg) TelegramMessages(context.Context) ([]notify.TelegramMessage, error) {
	return nil, nil
}
func (f *fakeTg) TelegramGetUpdates(_ context.Context, offset int64, _ int) ([]notify.TelegramUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []notify.TelegramUpdate
	for _, u := range f.updates {
		if u.UpdateID >= offset {
			out = append(out, u)
		}
	}
	return out, nil
}
func (f *fakeTg) sentCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }
func (f *fakeTg) lastSent() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return "", ""
	}
	return f.sent[len(f.sent)-1].chat, f.sent[len(f.sent)-1].text
}

func tgUpd(id int64, chat, text string) notify.TelegramUpdate {
	return notify.TelegramUpdate{UpdateID: id, Message: notify.TelegramMessage{
		Chat: notify.TelegramChat{ID: chat, Type: "group", Title: "Plant floor"},
		Text: text, Author: "op1",
	}}
}

func TestIntegrationTelegramInbound(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tgin")
	ctx := context.Background()
	pool := s.st.Pool
	t.Setenv("TELEGRAM_INBOUND", "1")
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	clean := func() {
		pool.Exec(ctx, `DELETE FROM telegram_chats WHERE tenant_id='itest-tgin'`)
		pool.Exec(ctx, `DELETE FROM telegram_pair_codes WHERE tenant_id='itest-tgin'`)
		pool.Exec(ctx, `DELETE FROM telegram_inbox WHERE chat_id IN ('111','222','333')`)
		pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-tgin'`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-tgin'`)
		pool.Exec(ctx, `UPDATE telegram_cursor SET last_update_id=0 WHERE id=1`)
		telegramRepliedAt = sync.Map{}
	}
	clean()
	t.Cleanup(clean)
	fake := &fakeTg{}
	s.notifier = fake

	// 1: a batch is stored and the cursor advances; re-polling from the cursor returns nothing new.
	fake.updates = []notify.TelegramUpdate{tgUpd(10, "111", "hello"), tgUpd(11, "111", "/help")}
	pool.Exec(ctx, `INSERT INTO telegram_chats(tenant_id,chat_id,title,chat_type) VALUES('itest-tgin','111','Plant floor','group')`)
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var cnt int
	pool.QueryRow(ctx, `SELECT count(*) FROM telegram_inbox WHERE chat_id='111'`).Scan(&cnt)
	if cnt != 2 {
		t.Fatalf("inbox rows = %d, want 2", cnt)
	}
	var last int64
	pool.QueryRow(ctx, `SELECT last_update_id FROM telegram_cursor WHERE id=1`).Scan(&last)
	if last != 11 {
		t.Fatalf("cursor = %d, want 11", last)
	}
	if fake.sentCount() != 1 {
		t.Fatalf("/help replies = %d, want 1", fake.sentCount())
	}
	chat, text := fake.lastSent()
	if chat != "111" || !strings.Contains(text, "/alerts") {
		t.Fatalf("help reply = %q to %q", text, chat)
	}

	// 2: same batch replayed (process crash before cursor commit simulation: reset cursor, replay)
	pool.Exec(ctx, `UPDATE telegram_cursor SET last_update_id=9 WHERE id=1`)
	telegramRepliedAt = sync.Map{}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM telegram_inbox WHERE chat_id='111'`).Scan(&cnt)
	if cnt != 2 {
		t.Fatalf("inbox rows after replay = %d, want 2 (ON CONFLICT)", cnt)
	}

	// 3: unpaired chat gets silence even for commands.
	fake.updates = []notify.TelegramUpdate{tgUpd(12, "222", "/alerts")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.sentCount() != 2 { // one help from step 1 + one duplicate help from step 2 replay
		t.Fatalf("replies after unpaired /alerts = %d, want 2", fake.sentCount())
	}

	// 4: /alerts from the paired chat lists open alerts for that tenant only.
	if _, err := pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,status) VALUES
		('a1','itest-tgin','critical','Boiler temp high','open'),
		('a2','itest-tgin','warning','Old resolved one','resolved')`); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES('itest-tgin2','itest-tgin2') ON CONFLICT DO NOTHING`)
	if _, err := pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,status) VALUES
		('a3','itest-tgin2','critical','Other tenant alert','open')`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-tgin2'`)
	telegramRepliedAt = sync.Map{}
	fake.updates = []notify.TelegramUpdate{tgUpd(13, "111", "/alerts")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	chat, text = fake.lastSent()
	if chat != "111" || !strings.Contains(text, "Boiler temp high") || !strings.Contains(text, "CRITICAL") {
		t.Fatalf("alerts reply = %q", text)
	}
	if strings.Contains(text, "Other tenant alert") || strings.Contains(text, "Old resolved one") {
		t.Fatalf("alerts reply leaked wrong rows: %q", text)
	}

	// 5: per-chat cooldown: a second command inside the window gets no reply.
	before := fake.sentCount()
	fake.updates = []notify.TelegramUpdate{tgUpd(14, "111", "/alerts")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.sentCount() != before {
		t.Fatalf("cooldown did not suppress second reply")
	}

	// 6: unknown commands get no reply.
	telegramRepliedAt = sync.Map{}
	fake.updates = []notify.TelegramUpdate{tgUpd(15, "111", "/shutdown now")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.sentCount() != before {
		t.Fatalf("unknown command got a reply")
	}

	// 7: pairing reads the inbox in poller mode: a spoken code pairs the chat.
	pool.Exec(ctx, `INSERT INTO telegram_pair_codes(tenant_id,code) VALUES('itest-tgin','PAIR42')`)
	pool.Exec(ctx, `INSERT INTO telegram_inbox(update_id,chat_id,chat_type,title,author,text) VALUES
		(16,'333','private','', 'newguy','/pair PAIR42')`)
	pool.Exec(ctx, `UPDATE telegram_cursor SET last_update_id=16 WHERE id=1`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/notifications/telegram/chats", s.telegramChats)
	w := callAs(mux, "itest-tgin", "test-user", "admin", "GET", "/v1/notifications/telegram/chats", "")
	if w.Code != 200 {
		t.Fatalf("chats status = %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "333") {
		t.Fatalf("paired chat 333 missing: %s", w.Body.String())
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM telegram_pair_codes WHERE tenant_id='itest-tgin'`).Scan(&left)
	if left != 0 {
		t.Fatalf("pair code not consumed")
	}
}


func TestIntegrationTelegramInboundRecovery(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tgrec")
	ctx := context.Background()
	pool := s.st.Pool
	t.Setenv("TELEGRAM_INBOUND", "1")
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	clean := func() {
		pool.Exec(ctx, `DELETE FROM telegram_chats WHERE tenant_id='itest-tgrec'`)
		pool.Exec(ctx, `DELETE FROM telegram_inbox WHERE chat_id='rec1'`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-tgrec'`)
		telegramRepliedAt = sync.Map{}
	}
	clean()
	t.Cleanup(clean)
	fake := &fakeTg{}
	s.notifier = fake
	pool.Exec(ctx, `INSERT INTO telegram_chats(tenant_id,chat_id,title,chat_type) VALUES('itest-tgrec','rec1','Ops','group')`)

	// Crash window 1: inbox row committed, process died before the reply. Recovery answers it once.
	pool.Exec(ctx, `INSERT INTO telegram_inbox(update_id,chat_id,chat_type,title,author,text) VALUES (90001,'rec1','group','Ops','op1','/help')`)
	s.telegramRecover(ctx)
	if fake.sentCount() != 1 {
		t.Fatalf("recovery replies = %d, want 1", fake.sentCount())
	}
	var answered bool
	pool.QueryRow(ctx, `SELECT answered_at IS NOT NULL FROM telegram_inbox WHERE update_id=90001`).Scan(&answered)
	if !answered {
		t.Fatalf("answered_at not recorded after recovery")
	}
	s.telegramRecover(ctx) // a second start does not answer again
	if fake.sentCount() != 1 {
		t.Fatalf("second recovery replied again")
	}

	// Crash window 2: Telegram accepted the send but the process died before recording
	// answered_at. The recovery scan CANNOT know the send happened, so it re-sends: this is the
	// documented single-duplicate tradeoff (Telegram has no idempotent send).
	pool.Exec(ctx, `INSERT INTO telegram_inbox(update_id,chat_id,chat_type,title,author,text) VALUES (90002,'rec1','group','Ops','op1','/help')`)
	telegramRepliedAt = sync.Map{}
	s.telegramRecover(ctx) // stands in for the pre-crash run's reply
	pool.Exec(ctx, `UPDATE telegram_inbox SET answered_at=NULL WHERE update_id=90002`) // the crash: recording lost, send really happened
	telegramRepliedAt = sync.Map{}
	s.telegramRecover(ctx)
	if fake.sentCount() != 3 {
		t.Fatalf("after accept-crash recovery total replies = %d, want 3 (one duplicate, documented)", fake.sentCount())
	}

	// Old unanswered rows are outside the recovery window: a node dead longer than that loses the reply.
	pool.Exec(ctx, `INSERT INTO telegram_inbox(update_id,chat_id,chat_type,title,author,text,received_at) VALUES (90003,'rec1','group','Ops','op1','/help', now() - interval '1 hour')`)
	telegramRepliedAt = sync.Map{}
	s.telegramRecover(ctx)
	if fake.sentCount() != 3 {
		t.Fatalf("stale unanswered row got a reply")
	}
}

func TestIntegrationTelegramInboundGuards(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tgg1")
	seed(t, s, "itest-tgg2")
	ctx := context.Background()
	pool := s.st.Pool
	t.Setenv("TELEGRAM_INBOUND", "1")
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	clean := func() {
		pool.Exec(ctx, `DELETE FROM telegram_chats WHERE tenant_id IN ('itest-tgg1','itest-tgg2')`)
		pool.Exec(ctx, `DELETE FROM telegram_pair_codes WHERE tenant_id IN ('itest-tgg1','itest-tgg2')`)
		pool.Exec(ctx, `DELETE FROM telegram_inbox WHERE chat_id IN ('g1')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-tgg1','itest-tgg2')`)
		telegramRepliedAt = sync.Map{}
	}
	clean()
	t.Cleanup(clean)
	fake := &fakeTg{}
	s.notifier = fake
	pool.Exec(ctx, `INSERT INTO telegram_chats(tenant_id,chat_id,title,chat_type) VALUES('itest-tgg1','g1','Ops','group')`)

	// Bot-authored commands are dropped at ingest: no inbox row, no reply.
	botUpd := tgUpd(50, "g1", "/alerts")
	botUpd.Message.FromBot = true
	fake.updates = []notify.TelegramUpdate{botUpd}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var cnt int
	pool.QueryRow(ctx, `SELECT count(*) FROM telegram_inbox WHERE chat_id='g1'`).Scan(&cnt)
	if cnt != 0 || fake.sentCount() != 0 {
		t.Fatalf("bot-authored message stored or answered (rows=%d sent=%d)", cnt, fake.sentCount())
	}

	// A second tenant cannot claim a chat already paired to tgg1 by speaking its own code there.
	pool.Exec(ctx, `INSERT INTO telegram_pair_codes(tenant_id,code) VALUES('itest-tgg2','CLAIM99')`)
	pool.Exec(ctx, `INSERT INTO telegram_inbox(update_id,chat_id,chat_type,title,author,text) VALUES (90051,'g1','group','Ops','mallory','/pair CLAIM99')`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/notifications/telegram/chats", s.telegramChats)
	w := callAs(mux, "itest-tgg2", "test-user", "admin", "GET", "/v1/notifications/telegram/chats", "")
	if w.Code != 200 {
		t.Fatalf("chats status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "g1") {
		t.Fatalf("second tenant claimed an already-paired chat: %s", w.Body.String())
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM telegram_chats WHERE chat_id='g1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("pairings for g1 = %d, want 1", n)
	}
	// tgg1's commands still answered; the claim attempt did not break the original pairing.
	fake.updates = []notify.TelegramUpdate{tgUpd(52, "g1", "/help")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.sentCount() != 1 {
		t.Fatalf("original tenant's /help not answered")
	}

	// A chat that ends up paired to two tenants anyway (legacy data) gets a refusal, never a guess.
	pool.Exec(ctx, `INSERT INTO telegram_chats(tenant_id,chat_id,title,chat_type) VALUES('itest-tgg2','g1','Ops','group')`)
	telegramRepliedAt = sync.Map{}
	fake.updates = []notify.TelegramUpdate{tgUpd(53, "g1", "/alerts")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.sentCount() != 1 {
		t.Fatalf("ambiguous chat got an answer")
	}

	// Unpairing stops replies immediately (the tenant lookup is per message).
	pool.Exec(ctx, `DELETE FROM telegram_chats WHERE chat_id='g1'`)
	telegramRepliedAt = sync.Map{}
	fake.updates = []notify.TelegramUpdate{tgUpd(54, "g1", "/alerts")}
	if err := s.telegramPollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.sentCount() != 1 {
		t.Fatalf("unpaired chat still answered")
	}
}
