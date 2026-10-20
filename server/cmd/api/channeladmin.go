package main

import (
	"context"
	"encoding/json"
	"net/http"
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

var telegramChatsAt sync.Map // tenant -> time of the last chat lookup (one per 10 s, per process)

// GET /v1/notifications/telegram/chats lists the chats the deployment's Telegram bot has seen,
// so an admin can pick a destination instead of hunting for a numeric chat id. Admin only, never
// through an API key or the assistant; rate limited like a channel test.
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
		TelegramChats(context.Context) ([]notify.TelegramChat, error)
	})
	if !ok2 {
		http.Error(w, "telegram not available", 503)
		return
	}
	chats, err := tgr.TelegramChats(r.Context())
	if err != nil {
		e := err.Error()
		if len(e) > 200 {
			e = e[:200]
		}
		writeJSON(w, 200, map[string]any{"chats": []any{}, "error": e})
		return
	}
	writeJSON(w, 200, map[string]any{"chats": chats})
}
