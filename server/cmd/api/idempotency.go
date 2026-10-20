package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Idempotency-Key support for POSTs the guided chat templates drive. A client that is unsure whether
// its request landed (timeout, dropped connection) retries with the same key and gets the first
// response replayed, instead of a second device, channel or invitation email.
//
// Rules: keys are scoped to (tenant, user, action, key). A key reused with a different body is a
// 409. A key whose request is still running is a 409, so concurrent duplicates cannot both write.
// Only responses below 500 are stored; a 5xx deletes the key so the retry really retries.
//
// Callers MUST run every authorization check (role, customer scope, quota-agnostic rules) BEFORE
// withIdempotency: replaying a stored response then cannot bypass a permission revoked since the
// first request.
type replayRecorder struct {
	header http.Header
	buf    bytes.Buffer
	code   int
}

func (r *replayRecorder) Header() http.Header { return r.header }
func (r *replayRecorder) Write(b []byte) (int, error) {
	return r.buf.Write(b)
}
func (r *replayRecorder) WriteHeader(c int) {
	if r.code == 0 {
		r.code = c
	}
}

func (s *server) withIdempotency(w http.ResponseWriter, r *http.Request, action string, payload []byte, fn func(w http.ResponseWriter)) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" { // no key: plain behaviour, exactly one attempt
		fn(w)
		return
	}
	if len(key) > 128 {
		http.Error(w, "Idempotency-Key too long", 400)
		return
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	t, u, ctx := auth.Tenant(r), auth.User(r), r.Context()
	tag, err := s.st.Pool.Exec(ctx, `INSERT INTO idempotency_keys(tenant_id,user_id,action,key,payload_hash) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, t, u, action, key, hash)
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	own := tag.RowsAffected() == 1 // we inserted the row: we run the write; otherwise inspect the existing row
	if own {
		s.runIdempotent(w, ctx, t, u, action, key, fn)
		return
	}
	var storedHash, status, body, ctype string
	var code int
	if err := s.st.Pool.QueryRow(ctx, `SELECT payload_hash,status,coalesce(response_code,0),coalesce(response_body,''),coalesce(response_type,'')
		FROM idempotency_keys WHERE tenant_id=$1 AND user_id=$2 AND action=$3 AND key=$4`, t, u, action, key).Scan(&storedHash, &status, &code, &body, &ctype); err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	if storedHash != hash {
		http.Error(w, "this Idempotency-Key was already used with a different request body", 409)
		return
	}
	if status == "in_flight" {
		http.Error(w, "a request with this Idempotency-Key is still running; retry in a moment", 409)
		return
	}
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.WriteHeader(code)
	w.Write([]byte(body)) //nolint:errcheck // replay of a response already produced once
}

// runIdempotent executes fn for the request that owns the key, records the outcome and flushes it.
func (s *server) runIdempotent(w http.ResponseWriter, ctx context.Context, t, u, action, key string, fn func(w http.ResponseWriter)) {
	rec := &replayRecorder{header: http.Header{}}
	fn(rec)
	if rec.code == 0 {
		rec.code = 200
	}
	if rec.code < 500 {
		s.st.Pool.Exec(ctx, `UPDATE idempotency_keys SET status='completed', response_code=$5, response_body=$6, response_type=$7
			WHERE tenant_id=$1 AND user_id=$2 AND action=$3 AND key=$4`, t, u, action, key, rec.code, rec.buf.String(), rec.header.Get("Content-Type"))
	} else { // a transient failure must stay retryable
		s.st.Pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id=$1 AND user_id=$2 AND action=$3 AND key=$4`, t, u, action, key)
	}
	// Keys are a retry aid, not a log: drop them after a day.
	s.st.Pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'`)
	for k, vs := range rec.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.code)
	w.Write(rec.buf.Bytes()) //nolint:errcheck
}
