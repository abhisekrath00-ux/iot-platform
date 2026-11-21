package ingestprocessing

import (
	"context"
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

var ErrInspectionHTTP = errors.New("staged inspection requires explicit engine and tracked session authority")

const inspectionPath = "/staged/processing-v1/inspect-input"

// InspectionSummary is retained processing evidence, never a delivery receipt or
// permission to run a batch. It deliberately omits input values, configuration,
// recipients, messages, plan bodies, credentials and attempt identifiers.
type InspectionSummary struct {
	State              string `json:"state"`
	Quarantine         string `json:"quarantine,omitempty"`
	AttemptStarted     bool   `json:"attempt_started"`
	PlannedOccurrences int    `json:"planned_occurrences"`
	BlockedStages      int    `json:"blocked_stages"`
	DeliveryConfirmed  bool   `json:"delivery_confirmed"`
	RetryPermitted     bool   `json:"retry_permitted"`
}

// NewGatewayInspectionHTTP is a separate default-off read-only controller. No
// startup installs it. Like the deny-only controller it accepts direct HTTPS and
// independently verified tracked HS256 admin sessions, never injected authority.
// It performs no persistent writes, execution, rearm, repair, or provider call.
// Live user/session rows and the scope table are share-locked until the retained
// snapshot is read. This briefly fences ALL scope edits. Authorization is valid
// for that transaction, not a guarantee that it remains valid after response.
// Historical records remain inspectable after gateway revocation or deletion.
func NewGatewayInspectionHTTP(enabled bool, engine Engine, sessionSecret []byte) (http.Handler, error) {
	if !enabled {
		return nil, ErrDisabled
	}
	if !engine.Enabled || engine.Pool == nil || len(sessionSecret) < 32 {
		return nil, ErrInspectionHTTP
	}
	secret := append([]byte(nil), sessionSecret...)
	slots := make(chan struct{}, 4)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "close")
		refuse := func(code int) { http.Error(w, "staged input inspection unavailable; no action was taken", code) }
		if r.Method != http.MethodPost || r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.URL.Path != inspectionPath || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
			refuse(404)
			return
		}
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
		controller := http.NewResponseController(w)
		if controller.SetReadDeadline(bodyDeadline) != nil || controller.SetWriteDeadline(deadline) != nil {
			refuse(503)
			return
		}
		// Decode exact string fields explicitly to reject duplicate and unknown keys,
		// nulls, arrays, trailing input and alternative spellings of field names.
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		start, err := dec.Token()
		if err != nil || start != json.Delim('{') {
			refuse(400)
			return
		}
		fields := map[string]string{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				refuse(400)
				return
			}
			key, ok := k.(string)
			if !ok || (key != "gateway_id" && key != "event_id" && key != "observed_at") {
				refuse(400)
				return
			}
			if _, exists := fields[key]; exists {
				refuse(400)
				return
			}
			v, err := dec.Token()
			value, ok := v.(string)
			if err != nil || !ok || len(value) == 0 || len(value) > 256 {
				refuse(400)
				return
			}
			fields[key] = value
		}
		if end, err := dec.Token(); err != nil || end != json.Delim('}') || dec.Decode(new(any)) != io.EOF || len(fields) != 3 {
			refuse(400)
			return
		}
		at, err := time.Parse(time.RFC3339Nano, fields["observed_at"])
		if err != nil || at.IsZero() || !at.Equal(at.Truncate(time.Microsecond)) {
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
		rec, err := read(ctx, tx, claims.TenantID, fields["event_id"], at)
		// Missing, foreign and corrupt evidence have the same non-disclosing result.
		if err != nil || rec.Plan.Envelope.GatewayID != fields["gateway_id"] {
			refuse(404)
			return
		}
		out := InspectionSummary{State: rec.State, Quarantine: rec.Quarantine, AttemptStarted: rec.AttemptID != "", PlannedOccurrences: len(rec.Plan.Intents), BlockedStages: len(rec.Plan.Blocked)}
		if err = tx.Commit(ctx); err != nil || ctx.Err() != nil {
			refuse(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}), nil
}
