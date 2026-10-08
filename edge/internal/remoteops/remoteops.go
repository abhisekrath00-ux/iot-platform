// Package remoteops handles the two remote maintenance requests an edge agent accepts from the server:
// send back its recent log lines, and restart the agent process. It cannot reboot the machine, run a
// shell, read other files or change configuration. Requests pass the same kind of gate as commands:
// strict JSON, expiry, short lifetime, one use per id, and (for restart) a named approver plus an
// explicit local opt-in in the agent's own config file.
package remoteops

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	KindLogs    = "logs"
	KindRestart = "restart"
	MaxLines    = 500
	maxTTL      = 10 * time.Minute
	skew        = 30 * time.Second
	ringLines   = 2000
	maxLineLen  = 500
	maxBytes    = 200 << 10
)

// Request is the wire format on t/<tenant>/g/<gw>/ops.
type Request struct {
	OpID       string    `json:"op_id"`
	Kind       string    `json:"kind"`
	Lines      int       `json:"lines,omitempty"`
	ApprovedBy string    `json:"approved_by,omitempty"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Result is published on t/<tenant>/g/<gw>/ops/result.
type Result struct {
	OpID   string    `json:"op_id"`
	Kind   string    `json:"kind"`
	OK     bool      `json:"ok"`
	Detail string    `json:"detail,omitempty"`
	Lines  []string  `json:"lines,omitempty"`
	At     time.Time `json:"at"`
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// Ring keeps the agent's most recent log lines in memory. It is an io.Writer so it can sit beside the
// normal log output. Nothing is written to disk.
type Ring struct {
	mu    sync.Mutex
	lines []string
	part  []byte
}

func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.part = append(r.part, p...)
	for {
		i := bytes.IndexByte(r.part, '\n')
		if i < 0 {
			break
		}
		r.add(string(r.part[:i]))
		r.part = r.part[i+1:]
	}
	if len(r.part) > 4*maxLineLen {
		r.add(string(r.part))
		r.part = nil
	}
	return len(p), nil
}

func (r *Ring) add(l string) {
	l = strings.TrimRight(l, "\r")
	if l == "" {
		return
	}
	if len(l) > maxLineLen {
		l = l[:maxLineLen] + "..."
	}
	r.lines = append(r.lines, l)
	if len(r.lines) > ringLines {
		r.lines = append([]string(nil), r.lines[len(r.lines)-ringLines:]...)
	}
}

var secretRe = regexp.MustCompile(`(?i)(pass(word|wd)?|secret|token|api[_-]?key|authorization|bearer|community|psk|private[_-]?key)(["']?\s*[:=]\s*|\s+)((bearer|basic)\s+)?\S+`)

// Redact hides values that follow secret-looking names. It is a safety net, not a promise: the agent
// is not meant to log secrets in the first place.
func Redact(l string) string {
	return secretRe.ReplaceAllString(l, "${1}=[redacted]")
}

// Tail returns the last n lines, redacted, capped in total size.
func (r *Ring) Tail(n int) []string {
	if n < 1 {
		n = 100
	}
	if n > MaxLines {
		n = MaxLines
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.lines
	if len(src) > n {
		src = src[len(src)-n:]
	}
	out := make([]string, 0, len(src))
	size := 0
	for i := len(src) - 1; i >= 0; i-- { // keep the newest when the cap bites
		l := Redact(src[i])
		size += len(l) + 1
		if size > maxBytes {
			break
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Handler validates and answers requests.
type Handler struct {
	Ring         *Ring
	AllowRestart bool // from the agent's own config; the server can never turn this on
	mu           sync.Mutex
	seen         map[string]time.Time
}

func NewHandler(r *Ring, allowRestart bool) *Handler {
	return &Handler{Ring: r, AllowRestart: allowRestart, seen: map[string]time.Time{}}
}

// Handle returns the result to publish (ok=false when there is no usable op_id to answer) and whether the
// agent must now restart itself, after the result has been sent.
func (h *Handler) Handle(payload []byte, now time.Time) (res Result, ok bool, restart bool) {
	res.At = now
	var probe struct {
		OpID string `json:"op_id"`
	}
	_ = json.Unmarshal(payload, &probe)
	res.OpID = probe.OpID
	reject := func(why string) (Result, bool, bool) {
		res.OK, res.Detail = false, why
		return res, idRe.MatchString(res.OpID), false
	}
	var q Request
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil || !idRe.MatchString(q.OpID) || q.IssuedAt.IsZero() || q.ExpiresAt.IsZero() {
		return reject("malformed request")
	}
	res.Kind = q.Kind
	if !now.Before(q.ExpiresAt) {
		return reject("request expired")
	}
	if q.IssuedAt.After(now.Add(skew)) {
		return reject("request issued in the future")
	}
	if q.ExpiresAt.Sub(q.IssuedAt) > maxTTL {
		return reject("request lifetime too long")
	}
	if q.Kind != KindLogs && q.Kind != KindRestart {
		return reject("unknown kind")
	}
	h.mu.Lock()
	for id, exp := range h.seen {
		if !now.Before(exp) {
			delete(h.seen, id)
		}
	}
	if _, dup := h.seen[q.OpID]; dup {
		h.mu.Unlock()
		return reject("request already seen")
	}
	h.seen[q.OpID] = q.ExpiresAt
	h.mu.Unlock()
	switch q.Kind {
	case KindLogs:
		res.OK, res.Lines = true, h.Ring.Tail(q.Lines)
		res.Detail = "last log lines since the agent started (secret-looking values hidden)"
	case KindRestart:
		if !h.AllowRestart {
			return reject("remote restart is not enabled in this gateway's config (remote_restart: true)")
		}
		if strings.TrimSpace(q.ApprovedBy) == "" {
			return reject("restart needs a named approver")
		}
		res.OK, res.Detail = true, "restarting the agent process; the service manager must start it again"
		return res, true, true
	}
	return res, true, false
}

var ErrNoSupervisor = errors.New("no service manager")
