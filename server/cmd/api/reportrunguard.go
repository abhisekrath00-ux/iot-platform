package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/jackc/pgx/v5"
)

var errRunReviewStale = errors.New("report or channel changed since review; close and review again")

type runReview struct {
	Token   string          `json:"token"`
	Report  json.RawMessage `json:"report"`
	Channel json.RawMessage `json:"channel"`
}

// Rows are fingerprinted with their transaction revision as well as values, so
// changing a value and changing it back still invalidates an earlier review.
func (s *server) readRunReview(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (runReview, error) {
	var reportJSON, channelJSON []byte
	var channelID *string
	var reportRev, channelRev string
	q := `SELECT jsonb_build_object('id',id,'name',name,'version',version,'definition',definition,'channel_id',channel_id,'schedule_cron',schedule_cron,'customer_id',customer_id),channel_id,xmin::text FROM reports WHERE tenant_id=$1 AND id=$2`
	if lock {
		q += ` FOR NO KEY UPDATE`
	}
	if err := tx.QueryRow(ctx, q, tenant, id).Scan(&reportJSON, &channelID, &reportRev); err != nil {
		return runReview{}, err
	}
	channelJSON = []byte(`null`)
	if channelID != nil {
		q = `SELECT to_jsonb(c),xmin::text FROM notification_channels c WHERE tenant_id=$1 AND id=$2`
		if lock {
			q += ` FOR SHARE`
		}
		err := tx.QueryRow(ctx, q, tenant, *channelID).Scan(&channelJSON, &channelRev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return runReview{}, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			channelJSON = []byte(`null`)
			channelRev = "missing"
		}
	}
	b, _ := json.Marshal([]string{tenant, id, string(reportJSON), reportRev, string(channelJSON), channelRev})
	h := sha256.Sum256(b)
	// Fingerprint full config, but only return the fields the UI needs.
	var safeChannel any
	if string(channelJSON) != "null" {
		var c map[string]any
		if err := json.Unmarshal(channelJSON, &c); err != nil {
			return runReview{}, err
		}
		safeChannel = map[string]any{"id": c["id"], "type": c["type"], "target": c["target"], "enabled": c["enabled"]}
	}
	publicChannel, _ := json.Marshal(safeChannel)
	return runReview{hex.EncodeToString(h[:]), reportJSON, publicChannel}, nil
}

func (s *server) reviewReportRun(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	if scopeOf(r) != "" {
		http.Error(w, "not available to customer-scoped users", 403)
		return
	}
	tx, err := s.st.Pool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		dbErr(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	review, err := s.readRunReview(r.Context(), tx, auth.Tenant(r), r.PathValue("id"), false)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "report not found", 404)
		return
	}
	if err != nil {
		dbErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, review)
}

// Report row lock serializes guarded runs and edits; channel SHARE lock blocks
// mutation/deletion while allowing other readers. Release only after send ends.
func (s *server) lockReviewedRun(ctx context.Context, tenant, id, expected string) (func(bool) error, error) {
	if len(expected) != 64 {
		return nil, errRunReviewStale
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return nil, errRunReviewStale
	}
	tx, err := s.st.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	release := func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		release()
		return nil, err
	}
	review, err := s.readRunReview(ctx, tx, tenant, id, true)
	if err != nil {
		release()
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errRunReviewStale
		}
		return nil, err
	}
	if review.Token != expected {
		release()
		return nil, errRunReviewStale
	}
	if string(review.Channel) != "null" {
		var c struct {
			Type    string
			Enabled bool
		}
		if err := json.Unmarshal(review.Channel, &c); err != nil {
			release()
			return nil, err
		}
		if !c.Enabled {
			release()
			return nil, errors.New("reviewed channel is disabled")
		}
		switch c.Type {
		case "email", "slack", "webhook", "teams", "sms", "whatsapp", "kafka", "amqp":
		default:
			release()
			return nil, errors.New("report delivery does not support this channel type")
		}
	} else {
		var r struct {
			ChannelID *string `json:"channel_id"`
		}
		_ = json.Unmarshal(review.Report, &r)
		if r.ChannelID != nil {
			release()
			return nil, errors.New("reviewed channel is unavailable")
		}
	}

	return func(success bool) error {
		if !success {
			release()
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE reports SET last_run_at=now() WHERE id=$1 AND tenant_id=$2`, id, tenant); err != nil {
			release()
			return err
		}
		err := tx.Commit(ctx)
		if err != nil {
			release()
		}
		return err
	}, nil
}
