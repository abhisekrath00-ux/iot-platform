package ingestprocessing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

var ErrQuarantineHTTP = errors.New("staged quarantine requires explicit engine and session verification authority")

// NewGatewayQuarantineHTTP returns a separate default-off deny-only controller.
// NO runtime/router installs it. It authenticates HS256 tracked interactive
// sessions itself, never trusting injected context/forwarding/body tenant claims.
// The caller must supply the existing trusted session issuer secret, direct HTTPS
// and bounded server limits. It grants/rotates nothing and never calls DenyLeaf:
// only the exact JWT tenant's current active edge/cert gateway is set revoked.
// Other gateways sharing the leaf are NOT globally denied. No unquarantine exists.
//
// The live user/session and exact gateway binding are locked in the SAME bounded
// serializable transaction as status+audit. Concurrent demotion/session revocation
// or binding changes precede this transaction or wait/fail it. A bounded table
// SHARE lock fences customer-scope inserts/deletes, including an absent scope row.
// This temporarily blocks ALL customer-scope edits, not just this user's. No
// migration/schema repair/production startup or retry is implied. Exact95 and
// restore continuity must be established separately before any future activation.
func NewGatewayQuarantineHTTP(enabled bool, engine Engine, sessionSecret []byte) (http.Handler, error) {
	if !enabled {
		return nil, ErrDisabled
	}
	if !engine.Enabled || engine.Pool == nil || len(sessionSecret) < 32 {
		return nil, ErrQuarantineHTTP
	}
	secret := append([]byte(nil), sessionSecret...)
	// Four active decisions maximum, including slow bodies and DB waiters. No
	// goroutine/queue is created for excess callers. This is not pre-TLS capacity.
	slots := make(chan struct{}, 4)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "close")
		refuse := func(code int) {
			http.Error(w, "staged gateway quarantine refused or uncertain; inspect current state", code)
		}
		if r.Method != http.MethodPost || r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.URL.Path != "/staged/processing-v1/quarantine-gateway" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
			refuse(404)
			return
		}
		// No proxy headers or loopback plaintext exemption. Context markings can
		// only restrict access; they can never establish an authenticated user.
		if r.TLS == nil || !r.TLS.HandshakeComplete || auth.ViaKey(r) || auth.ViaAI(r) {
			refuse(403)
			return
		}
		if len(r.Header.Values("Authorization")) != 1 {
			refuse(401)
			return
		}
		h := r.Header.Get("Authorization")
		if len(h) < 8 || len(h) > 8192 || h[:7] != "Bearer " {
			refuse(401)
			return
		}
		var claims auth.Claims
		token, err := jwt.ParseWithClaims(h[7:], &claims, func(*jwt.Token) (any, error) { return secret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
		if err != nil || !token.Valid || claims.Subject == "" || claims.TenantID == "" || claims.ID == "" || claims.IssuedAt == nil || claims.Role != "admin" {
			refuse(401)
			return
		}
		if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) != 0 || r.ContentLength > 2048 {
			refuse(400)
			return
		}
		if typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || typ != "application/json" {
			refuse(400)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			refuse(503)
			return
		}
		deadline := time.Now().Add(10 * time.Second)
		if claims.ExpiresAt.Time.Before(deadline) {
			deadline = claims.ExpiresAt.Time
		}
		bodyDeadline := time.Now().Add(3 * time.Second)
		if deadline.Before(bodyDeadline) {
			bodyDeadline = deadline
		}
		// Context cancellation alone cannot interrupt a blocked body read. The
		// actual connection must accept deadlines; unsupported wrappers refuse.
		controller := http.NewResponseController(w)
		if controller.SetReadDeadline(bodyDeadline) != nil || controller.SetWriteDeadline(deadline) != nil {
			refuse(503)
			return
		}
		var in struct {
			Gateway  string `json:"gateway_id"`
			Leaf     string `json:"expected_leaf_sha256"`
			Compared bool   `json:"compared_current_identity"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || !in.Compared || len(in.Gateway) == 0 || len(in.Gateway) > 256 {
			refuse(400)
			return
		}
		b, err := hex.DecodeString(in.Leaf)
		if err != nil || len(b) != 32 || in.Leaf != hex.EncodeToString(b) || in.Leaf == hex.EncodeToString(make([]byte, 32)) {
			refuse(400)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		tx, err := engine.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			refuse(503)
			return
		}
		defer rollback(tx)
		if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='3s';SET LOCAL statement_timeout='8s'"); err != nil {
			refuse(503)
			return
		}
		// Scope rows can be absent and their writers have no per-user locking
		// protocol. Acquire this conservative table fence BEFORE the first MVCC
		// snapshot, never infer that FOR SHARE on users locks absent scope rows.
		// This is deliberately bounded and NOT enabled in normal runtime.
		if _, err = tx.Exec(ctx, "LOCK TABLE user_customer_scope IN SHARE MODE NOWAIT"); err != nil {
			refuse(503)
			return
		}
		var role string
		var off, mustChange, scoped bool
		var cutoff *time.Time
		if err = tx.QueryRow(ctx, `SELECT role,disabled_at IS NOT NULL,must_change_credentials,tokens_valid_after,EXISTS(SELECT 1 FROM user_customer_scope c WHERE c.tenant_id=u.tenant_id AND c.user_id=u.id) FROM users u WHERE id=$1 AND tenant_id=$2 FOR SHARE OF u`, claims.Subject, claims.TenantID).Scan(&role, &off, &mustChange, &cutoff, &scoped); err != nil || role != "admin" || off || mustChange || scoped {
			refuse(403)
			return
		}
		var created time.Time
		var revoked *time.Time
		if err = tx.QueryRow(ctx, `SELECT created_at,revoked_at FROM user_sessions WHERE id=$1 AND tenant_id=$2 AND user_id=$3 FOR SHARE`, claims.ID, claims.TenantID, claims.Subject).Scan(&created, &revoked); err != nil || revoked != nil || (cutoff != nil && !created.After(*cutoff)) {
			refuse(403)
			return
		}
		var leaf [32]byte
		copy(leaf[:], b)
		if err = guardGatewayAuthority(ctx, tx, Envelope{TenantID: claims.TenantID, GatewayID: in.Gateway}, leaf); err != nil {
			refuse(409)
			return
		}
		tag, err := tx.Exec(ctx, `UPDATE gateways SET status='revoked' WHERE id=$1 AND tenant_id=$2 AND status='active' AND cert_fingerprint=$3`, in.Gateway, claims.TenantID, in.Leaf)
		if err != nil || tag.RowsAffected() != 1 {
			refuse(503)
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'staged.gateway_quarantined',$3,jsonb_build_object('expected_leaf_sha256',$4::text,'scope','current_gateway_only','global_leaf_denied',false))`, claims.TenantID, claims.Subject, in.Gateway, in.Leaf); err != nil {
			refuse(503)
			return
		}
		if err = tx.Commit(ctx); err != nil {
			refuse(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"gateway_quarantined","global_leaf_denied":false,"provider_rollback":false,"response_drain_confirmed":false}`))
	}), nil
}
