package main

// Slack and email access to the assistant.
//
// The hard part is knowing who is speaking. A bare email From line can be forged, so:
//   - the message must arrive signed (Slack: the app signing secret; email: an HMAC from the mail
//     gateway the admin set up) and fresh (5 minutes), and each message id is accepted once;
//   - email also needs the gateway to report dkim=pass and spf=pass for the sender;
//   - the sender must be a verified link to one platform user. Linking needs a one-time code that
//     the signed-in user gets in the web UI and then sends from that Slack account or mailbox;
//   - Slack is accepted in direct messages only, so answers never land in a shared channel.
// The message then runs as that user with that user's role. Changes wait for "yes" from the same
// identity (or run at once if an admin switched on low-risk auto-run for that link). Approving
// control commands is not possible from chat: the assistant runs as an API-key-like session.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
)

const (
	slackSecretName = "assistant-slack-signing"
	emailSecretName = "assistant-email-inbound"
	maxInbound      = 128 << 10
	linkCodeTTL     = 15 * time.Minute
)

type replier interface {
	Email(ctx context.Context, to []string, subject, body string) error
	Slack(ctx context.Context, channel, text string) error
}

func (s *server) replier() replier {
	if s.notifier != nil {
		return s.notifier
	}
	return notify.FromEnv()
}

// auditAs writes an audit row without an HTTP request (chat runs have none).
func (s *server) auditAs(ctx context.Context, tenant, user, action, target string, detail map[string]any) {
	detail["ai_initiated"] = true
	d, _ := json.Marshal(detail)
	s.st.Pool.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,$3,$4,$5)`, tenant, user, action, target, d)
}

// ---- admin settings ----

func (s *server) getChannelSettings(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		return
	}
	var se, ee bool
	var ss, es string
	s.st.Pool.QueryRow(r.Context(), `SELECT slack_enabled,email_enabled,slack_secret,email_secret FROM assistant_channel_settings WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&se, &ee, &ss, &es)
	t := auth.Tenant(r)
	writeJSON(w, 200, map[string]any{"slack_enabled": se, "email_enabled": ee, "has_slack_secret": ss != "", "has_email_secret": es != "",
		"secrets_available": s.secrets != nil && len(s.secrets.Key) > 0,
		"slack_url":         "/v1/assistant/inbound/slack/" + t, "email_url": "/v1/assistant/inbound/email/" + t})
}

func (s *server) putChannelSettings(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		SlackEnabled bool    `json:"slack_enabled"`
		EmailEnabled bool    `json:"email_enabled"`
		SlackSecret  *string `json:"slack_signing_secret"`
		EmailSecret  *string `json:"email_inbound_secret"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	tenant := auth.Tenant(r)
	var ss, es string
	s.st.Pool.QueryRow(r.Context(), `SELECT slack_secret,email_secret FROM assistant_channel_settings WHERE tenant_id=$1`, tenant).Scan(&ss, &es)
	put := func(p *string, name string, cur *string) bool {
		if p == nil || *p == "" {
			return true
		}
		if len(*p) < 16 {
			http.Error(w, "a signing secret must be at least 16 characters", 400)
			return false
		}
		st := s.secretStore(w)
		if st == nil {
			return false
		}
		if err := st.Put(r.Context(), tenant, name, auth.User(r), []byte(*p)); err != nil {
			http.Error(w, "could not store the secret", 500)
			return false
		}
		*cur = name
		return true
	}
	if !put(in.SlackSecret, slackSecretName, &ss) || !put(in.EmailSecret, emailSecretName, &es) {
		return
	}
	if (in.SlackEnabled && ss == "") || (in.EmailEnabled && es == "") {
		http.Error(w, "set the signing secret before enabling a channel", 400)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO assistant_channel_settings(tenant_id,slack_enabled,email_enabled,slack_secret,email_secret,updated_by) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id) DO UPDATE SET slack_enabled=EXCLUDED.slack_enabled, email_enabled=EXCLUDED.email_enabled, slack_secret=EXCLUDED.slack_secret, email_secret=EXCLUDED.email_secret, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		tenant, in.SlackEnabled, in.EmailEnabled, ss, es, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "assistant.channel_settings", tenant, map[string]any{"slack_enabled": in.SlackEnabled, "email_enabled": in.EmailEnabled})
	s.getChannelSettings(w, r)
}

// ---- links ----

var slackIDRe = regexp.MustCompile(`^[UW][A-Z0-9]{6,20}$`)

func hashCode(c string) string {
	h := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(c))))
	return hex.EncodeToString(h[:])
}

func normEmail(a string) (string, bool) {
	p, err := mail.ParseAddress(strings.TrimSpace(a))
	if err != nil || len(p.Address) > 254 {
		return "", false
	}
	return strings.ToLower(p.Address), true
}

// POST /v1/assistant/links {kind, address}: start linking a Slack member id or an email address to
// the signed-in user. The one-time code is shown once; the user sends "link <code>" from that identity.
func (s *server) createChannelLink(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "interactive session required", 403)
		return
	}
	var in struct{ Kind, Address string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	addr := strings.TrimSpace(in.Address)
	switch in.Kind {
	case "slack":
		addr = strings.ToUpper(addr)
		if !slackIDRe.MatchString(addr) {
			http.Error(w, "a Slack member id looks like U01ABCDEF (Profile > More > Copy member ID)", 400)
			return
		}
	case "email":
		var ok bool
		if addr, ok = normEmail(addr); !ok {
			http.Error(w, "not a valid email address", 400)
			return
		}
	default:
		http.Error(w, "kind must be slack or email", 400)
		return
	}
	tenant, user := auth.Tenant(r), auth.User(r)
	code := newCode() + newCode()[:2]
	tag, err := s.st.Pool.Exec(r.Context(), `INSERT INTO assistant_channel_links(id,tenant_id,user_id,kind,address,code_hash) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id,kind,address) DO UPDATE SET code_hash=EXCLUDED.code_hash, created_at=now()
		WHERE assistant_channel_links.user_id=EXCLUDED.user_id AND assistant_channel_links.status='pending'`,
		uuid.NewString(), tenant, user, in.Kind, addr, hashCode(code))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "that address is already linked or being linked by someone else", 409)
		return
	}
	s.audit(r, "assistant.link_start", in.Kind+":"+addr, nil)
	writeJSON(w, 201, map[string]any{"kind": in.Kind, "address": addr, "code": code, "expires_in_minutes": int(linkCodeTTL.Minutes()),
		"next": "From that " + in.Kind + " account, send the assistant: link " + code})
}

func (s *server) listChannelLinks(w http.ResponseWriter, r *http.Request) {
	tenant := auth.Tenant(r)
	q := `SELECT id,user_id,kind,address,status,autorun_low_risk,created_at FROM assistant_channel_links WHERE tenant_id=$1`
	args := []any{tenant}
	if auth.Role(r) != "admin" {
		q += ` AND user_id=$2`
		args = append(args, auth.User(r))
	}
	rows, err := s.st.Pool.Query(r.Context(), q+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, u, k, a, st string
		var ar bool
		var at time.Time
		rows.Scan(&id, &u, &k, &a, &st, &ar, &at)
		out = append(out, map[string]any{"id": id, "user_id": u, "kind": k, "address": a, "status": st, "autorun_low_risk": ar, "created_at": at})
	}
	writeJSON(w, 200, out)
}

func (s *server) deleteChannelLink(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "interactive session required", 403)
		return
	}
	q, args := `DELETE FROM assistant_channel_links WHERE id=$1 AND tenant_id=$2`, []any{r.PathValue("id"), auth.Tenant(r)}
	if auth.Role(r) != "admin" {
		q += ` AND user_id=$3`
		args = append(args, auth.User(r))
	}
	tag, err := s.st.Pool.Exec(r.Context(), q, args...)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "assistant.link_remove", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// PUT /v1/assistant/links/{id}/autorun {enabled} (admin): let low-risk changes (acknowledge or
// comment on an alert) from this linked identity run without a "yes". Off by default.
func (s *server) setLinkAutorun(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE assistant_channel_links SET autorun_low_risk=$3 WHERE id=$1 AND tenant_id=$2 AND status='verified'`, r.PathValue("id"), auth.Tenant(r), in.Enabled)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found or not verified yet", 404)
		return
	}
	s.audit(r, "assistant.link_autorun", r.PathValue("id"), map[string]any{"enabled": in.Enabled})
	writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "autorun_low_risk": in.Enabled})
}

// ---- inbound ----

func hmacHex(secret []byte, msg string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func fresh(ts string) bool {
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	d := time.Since(time.Unix(n, 0))
	return d > -5*time.Minute && d < 5*time.Minute
}

// channelSecret returns the signing secret for a channel, or false if the channel is off.
func (s *server) channelSecret(ctx context.Context, tenant, kind string) ([]byte, bool) {
	var en bool
	var name string
	col := map[string]string{"slack": "slack_enabled,slack_secret", "email": "email_enabled,email_secret"}[kind]
	if s.st.Pool.QueryRow(ctx, `SELECT `+col+` FROM assistant_channel_settings WHERE tenant_id=$1`, tenant).Scan(&en, &name) != nil || !en || name == "" {
		return nil, false
	}
	if s.secrets == nil || len(s.secrets.Key) == 0 {
		return nil, false
	}
	v, err := s.secrets.Get(ctx, tenant, name)
	return v, err == nil
}

func (s *server) firstSeen(ctx context.Context, tenant, id string) bool {
	s.st.Pool.Exec(ctx, `DELETE FROM assistant_inbound_seen WHERE seen_at < now() - interval '2 days'`)
	tag, err := s.st.Pool.Exec(ctx, `INSERT INTO assistant_inbound_seen(tenant_id,msg_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, tenant, id)
	return err == nil && tag.RowsAffected() == 1
}

// POST /v1/assistant/inbound/slack/{tenant}: Slack Events API. Public, but only acts on a valid signature.
func (s *server) inboundSlack(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInbound))
	secret, on := s.channelSecret(r.Context(), tenant, "slack")
	if err != nil || !on {
		http.NotFound(w, r)
		return
	}
	ts := r.Header.Get("X-Slack-Request-Timestamp")
	want := "v0=" + hmacHex(secret, "v0:"+ts+":"+string(body))
	if !fresh(ts) || !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Slack-Signature"))) {
		http.Error(w, "bad signature", 401)
		return
	}
	var in struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		EventID   string `json:"event_id"`
		Event     struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			BotID       string `json:"bot_id"`
			User        string `json:"user"`
			Channel     string `json:"channel"`
			ChannelType string `json:"channel_type"`
			Text        string `json:"text"`
		} `json:"event"`
	}
	if json.Unmarshal(body, &in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if in.Type == "url_verification" {
		writeJSON(w, 200, map[string]string{"challenge": in.Challenge})
		return
	}
	e := in.Event
	if in.Type != "event_callback" || e.Type != "message" || e.Subtype != "" || e.BotID != "" || e.ChannelType != "im" || e.User == "" || in.EventID == "" {
		w.WriteHeader(200) // not a direct message from a person: ignore
		return
	}
	if !s.firstSeen(r.Context(), tenant, "slack:"+in.EventID) {
		w.WriteHeader(200)
		return
	}
	w.WriteHeader(200) // Slack wants an answer within 3 seconds; the work continues
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.handleChat(tenant, "slack", strings.ToUpper(e.User), e.Channel, "", e.Text)
	}()
}

// POST /v1/assistant/inbound/email/{tenant}: the admin's mail gateway forwards mail for the
// assistant address as signed JSON. X-Timestamp and X-Signature = hex HMAC-SHA256(secret, ts + "." + body).
func (s *server) inboundEmail(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInbound))
	secret, on := s.channelSecret(r.Context(), tenant, "email")
	if err != nil || !on {
		http.NotFound(w, r)
		return
	}
	ts := r.Header.Get("X-Timestamp")
	if !fresh(ts) || !hmac.Equal([]byte(hmacHex(secret, ts+"."+string(body))), []byte(strings.ToLower(r.Header.Get("X-Signature")))) {
		http.Error(w, "bad signature", 401)
		return
	}
	var in struct {
		MessageID string `json:"message_id"`
		From      string `json:"from"`
		Subject   string `json:"subject"`
		Text      string `json:"text"`
		DKIM      string `json:"dkim"`
		SPF       string `json:"spf"`
		Auto      bool   `json:"auto_submitted"`
	}
	if json.Unmarshal(body, &in) != nil || in.MessageID == "" {
		http.Error(w, "bad json", 400)
		return
	}
	from, ok := normEmail(in.From)
	if !ok || in.Auto {
		w.WriteHeader(200)
		return
	}
	if !strings.EqualFold(in.DKIM, "pass") || !strings.EqualFold(in.SPF, "pass") {
		// A forged From line: never act, never reply to it.
		s.auditAs(r.Context(), tenant, "", "assistant.inbound_refused", "email:"+from, map[string]any{"reason": "sender not authenticated (dkim/spf)"})
		w.WriteHeader(200)
		return
	}
	if !s.firstSeen(r.Context(), tenant, "email:"+in.MessageID) {
		w.WriteHeader(200)
		return
	}
	w.WriteHeader(200)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.handleChat(tenant, "email", from, from, in.Subject, in.Text)
	}()
}

var (
	linkRe = regexp.MustCompile(`(?i)^link\s+([A-Z0-9]{8})$`)
	yesRe  = regexp.MustCompile(`(?i)^(yes|y|confirm)(?:\s+([A-Z0-9]{6}))?[.!]?$`)
	noRe   = regexp.MustCompile(`(?i)^(no|n|reject|cancel)(?:\s+([A-Z0-9]{6}))?[.!]?$`)
)

func firstLines(t string) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(t, "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), ">") {
			continue
		}
		if strings.HasPrefix(l, "On ") && strings.HasSuffix(strings.TrimSpace(l), "wrote:") {
			break
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// handleChat processes one verified-channel message from a sender id (Slack member id or email).
// to is where the reply goes (Slack DM channel or email address).
func (s *server) handleChat(tenant, kind, sender, to, subject, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), assistantTimeout()+30*time.Second)
	defer cancel()
	text = truncStr(firstLines(text), 8000)
	reply := func(msg string) {
		var err error
		if kind == "slack" {
			err = s.replier().Slack(ctx, to, msg)
		} else {
			sub := "Re: " + truncStr(strings.TrimSpace(subject), 120)
			if strings.TrimSpace(subject) == "" {
				sub = "Assistant"
			}
			err = s.replier().Email(ctx, []string{to}, sub, msg)
		}
		if err != nil {
			s.auditAs(ctx, tenant, "", "assistant.reply_failed", kind, map[string]any{"error": truncStr(err.Error(), 200)})
		}
	}
	if text == "" {
		return
	}
	// linking: proves the sender controls this identity, using a code only the signed-in user saw
	if m := linkRe.FindStringSubmatch(text); m != nil {
		var uid string
		err := s.st.Pool.QueryRow(ctx, `UPDATE assistant_channel_links SET status='verified', verified_at=now(), code_hash=''
			WHERE tenant_id=$1 AND kind=$2 AND address=$3 AND status='pending' AND code_hash=$4 AND created_at > now() - interval '15 minutes' RETURNING user_id`,
			tenant, kind, sender, hashCode(m[1])).Scan(&uid)
		if err != nil {
			s.auditAs(ctx, tenant, "", "assistant.link_failed", kind+":"+sender, map[string]any{})
			reply("That link code is wrong or expired. Start again in the web app.")
			return
		}
		s.auditAs(ctx, tenant, uid, "assistant.link_verified", kind+":"+sender, map[string]any{})
		reply("Linked. You can now ask me to do things here. I act with your permissions, ask you to confirm changes, and cannot approve control commands.")
		return
	}
	var uid, role string
	var autorun bool
	err := s.st.Pool.QueryRow(ctx, `SELECT l.user_id, u.role, l.autorun_low_risk FROM assistant_channel_links l JOIN users u ON u.id=l.user_id AND u.tenant_id=l.tenant_id
		WHERE l.tenant_id=$1 AND l.kind=$2 AND l.address=$3 AND l.status='verified'`, tenant, kind, sender).Scan(&uid, &role, &autorun)
	if err != nil {
		s.auditAs(ctx, tenant, "", "assistant.inbound_refused", kind+":"+sender, map[string]any{"reason": "not a linked identity"})
		if kind == "slack" { // email is never answered to an unlinked address
			reply("This Slack account is not linked to a user. Link it in the web app under Assistant.")
		}
		return
	}
	if m := yesRe.FindStringSubmatch(text); m != nil {
		s.chatDecide(ctx, tenant, uid, role, kind, m[2], true, reply)
		return
	}
	if m := noRe.FindStringSubmatch(text); m != nil {
		s.chatDecide(ctx, tenant, uid, role, kind, m[2], false, reply)
		return
	}
	c, ok := s.aiConfigFor(ctx, tenant)
	if !ok || !c.Enabled || c.BaseURL == "" || c.Model == "" {
		reply("No AI model is connected for this workspace.")
		return
	}
	if !allowRun(tenant + "|" + uid) {
		reply("Too many assistant runs this hour. Try again later.")
		return
	}
	cfg, err := s.llmConfig(ctx, tenant, c)
	if err != nil {
		reply("The assistant model is not reachable: " + err.Error())
		return
	}
	res := s.runAgent(ctx, cfg, tenant, uid, role, []llm.Message{{Role: "system", Content: systemPrompt(role)}, {Role: "user", Content: text}}, runCtx{via: kind, autorun: autorun})
	s.auditAs(ctx, tenant, uid, "assistant.channel_run", kind, map[string]any{"model": c.Model, "tool_calls": res.tools, "proposed_changes": len(res.pending), "via": kind})
	if res.err != nil {
		reply("I could not reach the model: " + truncStr(res.err.Error(), 200))
		return
	}
	var b strings.Builder
	b.WriteString(res.reply)
	for _, p := range res.pending {
		fmt.Fprintf(&b, "\n\nWaiting for you: %s (%s %s)\nReply YES %s to do it, or NO %s to drop it.", p.Summary, p.Method, p.Path, p.Code, p.Code)
	}
	if len(res.pending) == 1 {
		b.WriteString("\n(A plain YES works while this is the only open request.)")
	}
	reply(truncStr(b.String(), 3500))
}

func (s *server) chatDecide(ctx context.Context, tenant, uid, role, kind, code string, yes bool, reply func(string)) {
	if code == "" { // a bare yes/no only works when exactly one request from this chat is open
		var n int
		s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM assistant_actions WHERE tenant_id=$1 AND user_id=$2 AND via=$3 AND status='pending' AND created_at > now() - interval '30 minutes'`, tenant, uid, kind).Scan(&n)
		if n != 1 {
			reply("There is nothing waiting here, or more than one thing. Reply YES or NO followed by the six-character code.")
			return
		}
		s.st.Pool.QueryRow(ctx, `SELECT code FROM assistant_actions WHERE tenant_id=$1 AND user_id=$2 AND via=$3 AND status='pending' AND created_at > now() - interval '30 minutes'`, tenant, uid, kind).Scan(&code)
	}
	if !yes {
		tag, _ := s.st.Pool.Exec(ctx, `UPDATE assistant_actions SET status='rejected', decided_at=now() WHERE tenant_id=$1 AND user_id=$2 AND via=$3 AND status='pending' AND code=upper($4)`, tenant, uid, kind, code)
		if tag.RowsAffected() == 0 {
			reply("Nothing open with that code.")
			return
		}
		s.auditAs(ctx, tenant, uid, "assistant.reject", code, map[string]any{"via": kind})
		reply("Dropped.")
		return
	}
	id, st, hc, out, err := s.executeAction(ctx, tenant, uid, role, code, kind)
	if err != nil {
		reply("Nothing open with that code, it expired, or it is no longer allowed.")
		return
	}
	s.auditAs(ctx, tenant, uid, "assistant.confirm", id, map[string]any{"result_code": hc, "via": kind})
	if st == "executed" {
		reply(fmt.Sprintf("Done (HTTP %d).", hc))
	} else {
		reply(fmt.Sprintf("That did not work (HTTP %d): %s", hc, truncStr(string(out), 300)))
	}
}
