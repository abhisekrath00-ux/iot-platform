package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// Channel administration: test, enable/disable and delete. Admin only. A channel test goes through the same
// delivery code as a real alert, so "the test worked" means alerts will arrive. Delete is refused while
// anything still points at the channel, because escalation steps would otherwise be dropped silently.

var channelTestAt sync.Map // channel id -> time of the last test (one per 10 s, per process)

func (s *server) channelRow(w http.ResponseWriter, r *http.Request) (id, typ, target string, ok bool) {
	id = r.PathValue("id")
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT type, target FROM notification_channels WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).Scan(&typ, &target); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return "", "", "", false
	}
	return id, typ, target, true
}

// POST /v1/notifications/channels/{id}/test
func (s *server) testChannel(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		}
		return
	}
	id, typ, target, ok := s.channelRow(w, r)
	if !ok {
		return
	}
	if prev, loaded := channelTestAt.Load(id); loaded && time.Since(prev.(time.Time)) < 10*time.Second {
		http.Error(w, "wait a few seconds between tests", http.StatusTooManyRequests)
		return
	}
	channelTestAt.Store(id, time.Now())
	var n rules.Notifier = s.replier()
	err := rules.SendOne(r.Context(), n, typ, target, "test", "This is a test message from HexThings. If you can read it, alerts on this channel will arrive.", "channel.test")
	res := map[string]any{"ok": err == nil}
	if err != nil {
		e := err.Error()
		if len(e) > 200 {
			e = e[:200]
		}
		res["error"] = e
	}
	s.audit(r, "channel.test", id, map[string]any{"type": typ, "ok": err == nil})
	writeJSON(w, 200, res)
}

// PATCH /v1/notifications/channels/{id} {"enabled": bool}
func (s *server) patchChannel(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.Enabled == nil {
		http.Error(w, `{"enabled": true|false} required`, 400)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE notification_channels SET enabled=$3 WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r), *in.Enabled)
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "channel.update", r.PathValue("id"), map[string]any{"enabled": *in.Enabled})
	w.WriteHeader(204)
}

// DELETE /v1/notifications/channels/{id}
func (s *server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id, _, _, ok := s.channelRow(w, r)
	if !ok {
		return
	}
	t := auth.Tenant(r)
	var uses []string
	check := func(label, q string) {
		var n int
		if s.st.Pool.QueryRow(r.Context(), q, id, t).Scan(&n) == nil && n > 0 {
			uses = append(uses, label)
		}
	}
	check("escalation steps", `SELECT count(*) FROM escalation_steps WHERE channel_id=$1 AND tenant_id=$2`)
	check("on-call schedules", `SELECT count(*) FROM oncall_schedules WHERE $1 = ANY(channel_ids) AND tenant_id=$2`)
	check("scheduled reports", `SELECT count(*) FROM reports WHERE channel_id=$1 AND tenant_id=$2`)
	check("flows", `SELECT count(*) FROM flows WHERE tenant_id=$2 AND definition::text LIKE '%' || $1 || '%'`)
	check("flow versions", `SELECT count(*) FROM flow_versions WHERE tenant_id=$2 AND definition::text LIKE '%' || $1 || '%'`)
	if len(uses) > 0 {
		http.Error(w, "this channel is still used by: "+join(uses)+". Remove those first, or disable the channel instead.", http.StatusConflict)
		return
	}
	s.st.Pool.Exec(r.Context(), `DELETE FROM notification_channels WHERE id=$1 AND tenant_id=$2`, id, t)
	s.audit(r, "channel.delete", id, nil)
	w.WriteHeader(204)
}

func join(a []string) string {
	out := ""
	for i, x := range a {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

var telegramChatsAt sync.Map // tenant -> time of the last pairing scan (one per 10 s, per process)

// Telegram pairing. The bot token is operator-global, so seeing a chat in getUpdates or knowing its
// id proves nothing about who owns it. A tenant proves ownership by sending its one-time code to the
// bot inside its own chat; only paired chats can be listed, picked or manually entered as channels.
// Codes live 10 minutes and are single use.

const pairCodeTTL = 10 * time.Minute

// POST /v1/notifications/telegram/pair-code -> {"code": "...", "expires_in_minutes": 10}
func (s *server) telegramPairCode(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		}
		return
	}
	if !notify.TelegramConfigured() {
		http.Error(w, "Telegram is not configured on this deployment (the operator sets TELEGRAM_BOT_TOKEN)", 409)
		return
	}
	code := newPairCode()
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO telegram_pair_codes(tenant_id,code) VALUES($1,$2)
		 ON CONFLICT (tenant_id) DO UPDATE SET code=$2, created_at=now()`, auth.Tenant(r), code); err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	s.audit(r, "channel.telegram_pair_code", "", nil)
	writeJSON(w, 200, map[string]any{"code": code, "expires_in_minutes": int(pairCodeTTL.Minutes())})
}

const pairAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no easily confused characters

func newPairCode() string {
	b := make([]byte, 6)
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "PAIRME"
	}
	for i := range raw {
		b[i] = pairAlphabet[int(raw[i])%len(pairAlphabet)]
	}
	return string(b)
}

// GET /v1/notifications/telegram/chats scans the bot's recent inbound messages for this tenant's
// pair code, records any chat that spoke it, and returns only this tenant's paired chats. Admin
// only, never through an API key or the assistant; rate limited like a channel test.
func (s *server) telegramChats(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		}
		return
	}
	if !notify.TelegramConfigured() {
		http.Error(w, "Telegram is not configured on this deployment (the operator sets TELEGRAM_BOT_TOKEN)", 409)
		return
	}
	t := auth.Tenant(r)
	if prev, loaded := telegramChatsAt.Load(t); loaded && time.Since(prev.(time.Time)) < 10*time.Second {
		http.Error(w, "wait a few seconds between lookups", http.StatusTooManyRequests)
		return
	}
	telegramChatsAt.Store(t, time.Now())
	tgr, ok2 := s.replier().(interface {
		TelegramMessages(context.Context) ([]notify.TelegramMessage, error)
	})
	if !ok2 {
		http.Error(w, "telegram not available", 503)
		return
	}
	// A live code turns a spoken message into a pairing. No active code: just list what is paired.
	var code string
	var codeAt time.Time
	_ = s.st.Pool.QueryRow(r.Context(), `SELECT code, created_at FROM telegram_pair_codes WHERE tenant_id=$1`, t).Scan(&code, &codeAt)
	codeLive := code != "" && time.Since(codeAt) < pairCodeTTL
	if codeLive {
		msgs, err := tgr.TelegramMessages(r.Context())
		if err != nil {
			e := err.Error()
			if len(e) > 200 {
				e = e[:200]
			}
			writeJSON(w, 200, map[string]any{"chats": []any{}, "error": e})
			return
		}
		for _, m := range msgs {
			if !strings.Contains(m.Text, code) {
				continue
			}
			if _, err := s.st.Pool.Exec(r.Context(),
				`INSERT INTO telegram_chats(tenant_id,chat_id,title,chat_type) VALUES($1,$2,$3,$4)
				 ON CONFLICT (tenant_id,chat_id) DO UPDATE SET title=$3, chat_type=$4, paired_at=now()`,
				t, m.Chat.ID, m.Chat.Title, m.Chat.Type); err == nil {
				s.st.Pool.Exec(r.Context(), `DELETE FROM telegram_pair_codes WHERE tenant_id=$1`, t)
				s.audit(r, "channel.telegram_paired", m.Chat.ID, map[string]any{"title": m.Chat.Title})
			}
		}
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT chat_id, chat_type, title FROM telegram_chats WHERE tenant_id=$1 ORDER BY paired_at DESC LIMIT 50`, t)
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer rows.Close()
	chats := []notify.TelegramChat{}
	for rows.Next() {
		var c notify.TelegramChat
		if rows.Scan(&c.ID, &c.Type, &c.Title) == nil {
			chats = append(chats, c)
		}
	}
	writeJSON(w, 200, map[string]any{"chats": chats, "pairing": codeLive})
}
