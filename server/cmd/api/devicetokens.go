package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Device tokens: hxd_<id>.<secret>. A token is bound to one device of one
// tenant and can only POST readings to /v1/device/ingest. It cannot read data,
// call any other endpoint, or reach another device. Tokens always expire.

const maxDeviceTokenDays = 730

func newDeviceToken() (id, secret, token string, err error) {
	b := make([]byte, 8+32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	id = hex.EncodeToString(b[:8])
	secret = base64.RawURLEncoding.EncodeToString(b[8:])
	return id, secret, "hxd_" + id + "." + secret, nil
}

func (s *server) resolveDeviceToken(ctx context.Context, token string) (tenant, device string, ok bool) {
	rest, found := strings.CutPrefix(token, "hxd_")
	id, secret, cut := strings.Cut(rest, ".")
	if !found || !cut || id == "" || secret == "" || len(id) > 32 || len(secret) > 128 {
		return "", "", false
	}
	var hash []byte
	err := s.st.Pool.QueryRow(ctx,
		`SELECT tenant_id, device_id, secret_hash FROM device_tokens WHERE id=$1 AND revoked_at IS NULL AND expires_at > now()`, id).Scan(&tenant, &device, &hash)
	if err != nil || subtle.ConstantTimeCompare(hash, hashSecret(secret)) != 1 {
		return "", "", false
	}
	go s.st.Pool.Exec(context.Background(), `UPDATE device_tokens SET last_used_at=now() WHERE id=$1`, id)
	return tenant, device, true
}

// deviceIngest is the unauthenticated-by-session route: the bearer device token
// fixes tenant and device, so the body cannot name another device.
func (s *server) deviceIngest(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hexmon-device"`)
		http.Error(w, "device token required", http.StatusUnauthorized)
		return
	}
	tenant, device, ok := s.resolveDeviceToken(r.Context(), tok)
	if !ok {
		http.Error(w, "invalid, expired or revoked token", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		Readings []ingestReading `json:"readings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Readings) == 0 {
		http.Error(w, "bad body: readings required", 400)
		return
	}
	if len(in.Readings) > maxIngestBatch {
		http.Error(w, "batch too large", http.StatusRequestEntityTooLarge)
		return
	}
	s.ingestBatch(w, r, tenant, device, in.Readings)
}

func (s *server) createDeviceToken(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		http.Error(w, "admin session required", 403)
		return
	}
	dev := r.PathValue("id")
	var in struct {
		Name string `json:"name"`
		Days int    `json:"expires_in_days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || in.Days <= 0 || in.Days > maxDeviceTokenDays {
		http.Error(w, "name (max 80) and expires_in_days (1-730) required", 400)
		return
	}
	var one int
	if s.st.Pool.QueryRow(r.Context(), `SELECT 1 FROM devices WHERE id=$1 AND tenant_id=$2`, dev, auth.Tenant(r)).Scan(&one) != nil {
		http.Error(w, "unknown device", 404)
		return
	}
	id, secret, token, err := newDeviceToken()
	if err != nil {
		http.Error(w, "entropy failure", 500)
		return
	}
	exp := time.Now().Add(time.Duration(in.Days) * 24 * time.Hour)
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO device_tokens(id,tenant_id,device_id,name,secret_hash,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, auth.Tenant(r), dev, in.Name, hashSecret(secret), auth.User(r), exp); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "devicetoken.create", dev, map[string]any{"token_id": id, "name": in.Name, "expires_at": exp})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"id": id, "device_id": dev, "name": in.Name, "expires_at": exp, "token": token,
		"note": "Store this token now. It cannot be shown again."})
}

func (s *server) listDeviceTokens(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		http.Error(w, "admin session required", 403)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id,name,created_by,created_at,expires_at,revoked_at,last_used_at FROM device_tokens WHERE tenant_id=$1 AND device_id=$2 ORDER BY created_at DESC`,
		auth.Tenant(r), r.PathValue("id"))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, by string
		var c, e time.Time
		var rv, lu *time.Time
		if rows.Scan(&id, &name, &by, &c, &e, &rv, &lu) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "created_by": by, "created_at": c, "expires_at": e, "revoked_at": rv, "last_used_at": lu})
		}
	}
	writeJSON(w, 200, out)
}

func (s *server) revokeDeviceToken(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		http.Error(w, "admin session required", 403)
		return
	}
	ct, err := s.st.Pool.Exec(r.Context(),
		`UPDATE device_tokens SET revoked_at=now() WHERE id=$1 AND tenant_id=$2 AND device_id=$3 AND revoked_at IS NULL`,
		r.PathValue("tid"), auth.Tenant(r), r.PathValue("id"))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "devicetoken.revoke", r.PathValue("id"), map[string]any{"token_id": r.PathValue("tid")})
	w.WriteHeader(204)
}
