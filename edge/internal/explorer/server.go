// Package explorer implements the interactive, read-only Modbus explorer.
// It is deliberately not mounted on the optional LAN status listener. Callers
// must supply an already-owned connection and a literal loopback listener.
package explorer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var page string

// Connection is a snapshot of an owned connection. ID must change when the
// connection is replaced, even if the configured device name is unchanged.
type Connection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Transport string    `json:"transport"`
	Endpoint  string    `json:"endpoint"`
	Unit      int       `json:"unit"`
	Temporary bool      `json:"temporary,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Range always uses the zero-based wire offset, not a 40001-style reference.
type Range struct {
	Connection string `json:"connection"`
	Function   int    `json:"function"`
	Offset     int    `json:"offset"`
	Count      int    `json:"count"`
}

func (r Range) Validate() error {
	if r.Connection == "" || len(r.Connection) > 128 || r.Function < 1 || r.Function > 4 || r.Offset < 0 || r.Offset > 65535 || r.Count < 1 || r.Count > 32 || r.Offset+r.Count > 65536 {
		return fmt.Errorf("choose a connection, FC1-4 and 1-32 wire addresses inside 0-65535")
	}
	return nil
}

// Source owns driver lifecycle and exclusive bus access. Read must never open
// another handle to an already-owned port, queue behind a poll, retry, or write.
// It must reject stale connection IDs and return at most one bounded response.
// Context cancellation is best effort for a transaction already on the wire;
// the driver must have its own finite I/O timeout.
type Source interface {
	Connections() []Connection
	Read(context.Context, Range) ([]uint16, error)
}

type reply struct {
	Range
	Words   []uint16  `json:"words,omitempty"`
	Error   string    `json:"error,omitempty"`
	Expired bool      `json:"expired,omitempty"`
	At      time.Time `json:"at"`
	Latency int64     `json:"latency_ms"`
}

type Server struct {
	host, origin, token string
	source              Source
	pending             sync.Mutex
}

// New refuses a hostname or wildcard bind: literal loopback plus an explicit
// port is required to fence Host-header/DNS-rebinding and foreign-origin reads.
// The supplied address must equal the actual listener address.
func New(address string, source Source) (*Server, error) {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || port == "" || port == "0" || source == nil {
		return nil, fmt.Errorf("explorer needs an owned source and literal loopback listener address")
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return nil, err
	}
	return &Server{host: address, origin: "http://" + address, token: hex.EncodeToString(raw), source: source}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(remote)
	if err != nil || ip == nil || !ip.IsLoopback() || r.Host != s.host {
		http.Error(w, "local explorer only", 403)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != s.origin {
		http.Error(w, "foreign origin refused", 403)
		return
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "none" && site != "same-origin" {
		http.Error(w, "foreign page refused", 403)
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters refused", 400)
		return
	}
	if r.URL.Path == "/" {
		if r.Method != "GET" {
			http.Error(w, "GET only", 405)
			return
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, strings.Replace(page, "SESSION_TOKEN", s.token, 1))
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Explorer-Session")), []byte(s.token)) != 1 {
		http.Error(w, "reopen the local explorer page", 403)
		return
	}
	switch r.URL.Path {
	case "/api/connections":
		if r.Method != "GET" {
			http.Error(w, "GET only", 405)
			return
		}
		h.Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.source.Connections())
	case "/api/connect", "/api/disconnect":
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		if origin != s.origin {
			http.Error(w, "same-origin request required", 403)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON only", 415)
			return
		}
		manager, ok := s.source.(interface {
			OpenTemporary(context.Context, Connect) (Connection, error)
			DisconnectTemporary(string) error
		})
		if !ok {
			http.Error(w, "temporary connections unavailable", 409)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req Connect
		var closeReq struct {
			ID string `json:"id"`
		}
		var target any = &req
		if r.URL.Path == "/api/disconnect" {
			target = &closeReq
		}
		if err := dec.Decode(target); err != nil {
			http.Error(w, "invalid connection request", 400)
			return
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			http.Error(w, "one connection request required", 400)
			return
		}
		if !s.pending.TryLock() {
			http.Error(w, "a read or connection change is in progress; nothing was queued", 409)
			return
		}
		defer s.pending.Unlock()
		if r.URL.Path == "/api/disconnect" {
			if closeReq.ID == "" || len(closeReq.ID) > 128 {
				http.Error(w, "choose a temporary connection", 400)
				return
			}
			if err := manager.DisconnectTemporary(closeReq.ID); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			w.WriteHeader(204)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		info, err := manager.OpenTemporary(ctx, req)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		h.Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	case "/api/read":
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		if origin != s.origin {
			http.Error(w, "same-origin request required", 403)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON only", 415)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req Range
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid read request", 400)
			return
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			http.Error(w, "one read request required", 400)
			return
		}
		if err := req.Validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if !s.pending.TryLock() {
			http.Error(w, "another read is in progress; no request was queued", 409)
			return
		}
		defer s.pending.Unlock()
		started := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		words, err := s.source.Read(ctx, req)
		res := reply{Range: req, At: time.Now().UTC(), Latency: time.Since(started).Milliseconds()}
		if err == nil && len(words) != req.Count {
			err = fmt.Errorf("response count did not match selected range")
		}
		if err == nil && req.Function <= 2 {
			for _, v := range words {
				if v > 1 {
					err = fmt.Errorf("invalid bit response")
					break
				}
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			res.Error = err.Error()
			res.Expired = errors.Is(err, ErrConnectionExpired)
		} else {
			res.Words = words
		}
		h.Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	default:
		http.NotFound(w, r)
	}
}
