package main

// Inbound Telegram agent: optional, off by default (TELEGRAM_INBOUND=1 plus the operator's
// TELEGRAM_BOT_TOKEN). One leader-elected poller long-polls the bot's getUpdates feed - Telegram
// forbids getUpdates while a webhook is set, so this poller is the ONLY consumer and the bot
// never sets a webhook. Every consumed update lands in telegram_inbox and the cursor advances in
// the same transaction, so a crash or failover neither skips an update nor answers one twice.
//
// Replies are deliberately narrow: this is an alerting bot, not a chat login. Only chats paired
// to a tenant (migration 0080, ownership proven with a one-time code) get answers, and only
// read-only commands exist - /alerts lists that tenant's open alerts, /help explains the bot.
// Unpaired chats get silence: replying would confirm the bot serves the deployment, and pairing
// messages themselves must never be echoed back. Writes (ack, resolve, control) stay in the web
// UI or the signed Slack/email assistant channels. Bot-authored messages are dropped at ingest:
// no bot-to-bot loops and no spoofed commands.
//
// Exactly-once replies do not exist here - Telegram offers no idempotent send, so this is the
// real tradeoff: inbox row + cursor commit BEFORE any reply (a crash there would lose the reply,
// which the startup recovery scan below re-sends from answered_at IS NULL rows); a crash AFTER
// Telegram accepted the send but BEFORE answered_at was recorded makes the recovery scan send
// ONE duplicate. Recovery only looks back tgRecoverWindow, so a node that stays dead longer
// loses the reply. Losing a /alerts answer is harmless (ask again); a duplicate is cosmetic.
//
// Pairing scopes disclosure to the chat, not to each member: a paired GROUP chat's /alerts
// answer is visible to everyone in that group, including people who join later - that is the
// point of an alert group, and admins must pair only groups that may see the site's alerts.
// One chat pairs to one tenant only; unpairing (deleting the pairing) stops replies at once,
// because the tenant lookup happens per message.

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/jackc/pgx/v5"
)

// leaderTelegramInbound is the advisory lock key for the Telegram inbound poller.
const leaderTelegramInbound int64 = 0x4845584D4F54

// telegramInboundEnabled reports whether the operator switched the inbound agent on.
func telegramInboundEnabled() bool {
	return notify.TelegramConfigured() && telegramInboundEnv()
}

func telegramInboundEnv() bool { return os.Getenv("TELEGRAM_INBOUND") == "1" }

const (
	tgPollTimeoutSec  = 25 // Bot API long-poll window
	tgRecoverWindow   = 10 * time.Minute
	tgInboxKeep       = 7 * 24 * time.Hour
	tgReplyCooldown   = 5 * time.Second
	tgMaxReplyAlerts  = 5
	tgCommandDeadline = 10 * time.Second
)

var telegramRepliedAt sync.Map // chat id -> last reply time (per-process; the poller is a singleton)

// telegramInbound is the leader job: poll, store, answer. It blocks in the long poll, so a quiet
// deployment costs one idle HTTP request per bot.
func (s *server) telegramInbound(ctx context.Context) {
	log.Printf("telegram-inbound: poller started")
	s.telegramRecover(ctx)
	for ctx.Err() == nil {
		if err := s.telegramPollOnce(ctx); err != nil {
			log.Printf("telegram-inbound: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// telegramRecover reprocesses recent messages the poller stored but never finished with - the
// crash window between the inbox commit and the reply. See the header for the duplicate risk.
func (s *server) telegramRecover(ctx context.Context) {
	rows, err := s.st.Pool.Query(ctx,
		`SELECT update_id, chat_id, chat_type, title, author, text FROM telegram_inbox
		 WHERE text LIKE '/%' AND answered_at IS NULL AND received_at > now() - $1::interval
		 ORDER BY update_id`, tgRecoverWindow.String())
	if err != nil {
		log.Printf("telegram-inbound: recovery scan: %v", err)
		return
	}
	var pending []notify.TelegramMessage
	for rows.Next() {
		var m notify.TelegramMessage
		if rows.Scan(new(int64), &m.Chat.ID, &m.Chat.Type, &m.Chat.Title, &m.Author, &m.Text) == nil {
			pending = append(pending, m)
		}
	}
	rows.Close()
	for _, m := range pending {
		if s.telegramAnswer(ctx, m) {
			markTelegramAnswered(ctx, s, m)
		}
	}
	if len(pending) > 0 {
		log.Printf("telegram-inbound: recovered %d stored messages", len(pending))
	}
}

// telegramPollOnce fetches one batch (blocking up to the long-poll window), stores it with the
// cursor in one transaction, then answers commands. Exported for tests via the package.
func (s *server) telegramPollOnce(ctx context.Context) error {
	var last int64
	err := s.st.Pool.QueryRow(ctx, `SELECT last_update_id FROM telegram_cursor WHERE id=1`).Scan(&last)
	if err == pgx.ErrNoRows {
		if _, err = s.st.Pool.Exec(ctx, `INSERT INTO telegram_cursor(id,last_update_id) VALUES(1,0)`); err == nil {
			last = 0
		}
	}
	if err != nil {
		return err
	}
	type updateGetter interface {
		TelegramGetUpdates(context.Context, int64, int) ([]notify.TelegramUpdate, error)
	}
	n, ok := s.replier().(updateGetter)
	if !ok {
		n = notify.FromEnv() // *notify.Notifier always implements updateGetter
	}
	ups, err := n.TelegramGetUpdates(ctx, last+1, tgPollTimeoutSec)
	if err != nil {
		return err
	}
	if len(ups) == 0 {
		return nil
	}
	tx, err := s.st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, u := range ups {
		if !u.Message.FromBot {
			if _, err := tx.Exec(ctx,
				`INSERT INTO telegram_inbox(update_id,chat_id,chat_type,title,author,text) VALUES($1,$2,$3,$4,$5,$6)
				 ON CONFLICT (update_id) DO NOTHING`,
				u.UpdateID, u.Message.Chat.ID, u.Message.Chat.Type, u.Message.Chat.Title, u.Message.Author, clipTg(u.Message.Text, 4000)); err != nil {
				return err
			}
		}
		if u.UpdateID > last {
			last = u.UpdateID
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE telegram_cursor SET last_update_id=$1 WHERE id=1`, last); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_inbox WHERE received_at < now() - $1::interval`, tgInboxKeep.String()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, u := range ups {
		if u.Message.FromBot {
			continue
		}
		if s.telegramAnswer(ctx, u.Message) {
			markTelegramAnswered(ctx, s, u.Message)
		}
	}
	return nil
}

// markTelegramAnswered records processing as finished for the newest inbox row matching this
// message's chat+text (the one this batch just stored).
func markTelegramAnswered(ctx context.Context, s *server, m notify.TelegramMessage) {
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.st.Pool.Exec(cctx, `UPDATE telegram_inbox SET answered_at=now()
		WHERE update_id=(SELECT max(update_id) FROM telegram_inbox WHERE chat_id=$1 AND text=$2)`, m.Chat.ID, clipTg(m.Text, 4000))
	_ = ctx
}

// telegramAnswer replies to a command from a paired chat, at most one reply per chat per
// cooldown. Anything else is ignored by design.
// telegramAnswer answers one command. It reports whether processing is finished (reply sent or
// deliberately skipped) so the caller can set answered_at; a failed send returns false and the
// startup recovery scan will try again within tgRecoverWindow.
func (s *server) telegramAnswer(ctx context.Context, m notify.TelegramMessage) bool {
	text := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(text, "/") {
		return true
	}
	cmd := strings.ToLower(strings.Fields(text)[0])
	if i := strings.Index(cmd, "@"); i > 0 { // /alerts@SomeBot
		cmd = cmd[:i]
	}
	if cmd != "/alerts" && cmd != "/help" && cmd != "/start" {
		return true
	}
	var tenants []string
	rows, err := s.st.Pool.Query(ctx, `SELECT tenant_id FROM telegram_chats WHERE chat_id=$1`, m.Chat.ID)
	if err != nil {
		return true
	}
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			tenants = append(tenants, t)
		}
	}
	rows.Close()
	if len(tenants) == 0 {
		return true // unpaired or unpaired-since: silence is the answer
	}
	if len(tenants) > 1 {
		// Pairing refuses a chat already paired elsewhere, so this should be impossible; never
		// guess which tenant's alerts a shared chat may read.
		log.Printf("telegram-inbound: chat %s paired to %d tenants, refusing to answer", m.Chat.ID, len(tenants))
		return true
	}
	tenant := tenants[0]
	if prev, loaded := telegramRepliedAt.Load(m.Chat.ID); loaded && time.Since(prev.(time.Time)) < tgReplyCooldown {
		return true
	}
	telegramRepliedAt.Store(m.Chat.ID, time.Now())
	cctx, cancel := context.WithTimeout(ctx, tgCommandDeadline)
	defer cancel()
	var reply string
	switch cmd {
	case "/alerts":
		reply = s.telegramAlerts(cctx, tenant)
	default: // /help, /start
		reply = "HexThings alerts bot. Commands: /alerts - current open alerts for this site; /help - this message. " +
			"Everything else (acknowledging, resolving, changing anything) is in the web UI."
	}
	tg, ok := s.replier().(interface {
		Telegram(context.Context, string, string) error
	})
	if !ok {
		return false
	}
	if err := tg.Telegram(cctx, m.Chat.ID, reply); err != nil {
		log.Printf("telegram-inbound: reply to chat %s failed: %v", m.Chat.ID, err)
		return false
	}
	s.auditAs(cctx, tenant, "telegram-inbound", "channel.telegram_inbound_reply", m.Chat.ID, map[string]any{"command": cmd})
	return true
}

// telegramAlerts renders the tenant's open alerts, newest first, capped and clipped: a chat
// message is not a dashboard.
func (s *server) telegramAlerts(ctx context.Context, tenant string) string {
	rows, err := s.st.Pool.Query(ctx,
		`SELECT severity, message, created_at FROM alerts WHERE tenant_id=$1 AND status='open'
		 ORDER BY created_at DESC LIMIT $2`, tenant, tgMaxReplyAlerts)
	if err != nil {
		return "Alerts are unavailable right now; try again in a minute."
	}
	defer rows.Close()
	var b strings.Builder
	n := 0
	for rows.Next() {
		var sev, msg string
		var at time.Time
		if rows.Scan(&sev, &msg, &at) != nil {
			continue
		}
		n++
		b.WriteString("- [" + strings.ToUpper(sev) + "] " + clipTg(msg, 120) + " (" + time.Since(at).Round(time.Minute).String() + " ago)\n")
	}
	if n == 0 {
		return "No open alerts right now."
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func clipTg(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
