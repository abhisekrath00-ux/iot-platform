package admissionoutbox

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// PutBatch retains one bounded complete sample in ONE SQLite transaction, or
// returns no saved evidence. It never sends, evicts or changes the schema.
// Identity repeats within the request refuse even if equal; exact rows already
// retained count as duplicates, not new capacity. Any uncertain commit requires
// an explicit retry of the SAME complete batch. This is not durability of a
// source read before commit, a durable retry intention, or a power-loss proof.
func (s *Store) PutBatch(ctx context.Context, readings []Reading) ([]Item, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if ctx == nil || len(readings) < 1 || len(readings) > 256 {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := make([]Item, len(readings))
	seen := make(map[[2]string]bool, len(readings))
	for i, r := range readings {
		b, d, e := canonical(r)
		if e != nil {
			return nil, e
		}
		key := [2]string{r.EventID, r.ObservedAt.Format(time.RFC3339Nano)}
		if seen[key] {
			return nil, ErrConflict
		}
		seen[key] = true
		topic := "t/" + r.TenantID + "/g/" + r.GatewayID + "/telemetry"
		if len(topic) > 540 {
			return nil, ErrInput
		}
		items[i] = Item{Topic: topic, Reading: r, Payload: b, InputSHA256: d}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	fresh := make([]int, 0, len(items))
	for i := range items {
		it := &items[i]
		var topic, digest string
		var payload []byte
		e = tx.QueryRowContext(ctx, `SELECT sequence,topic,payload,digest FROM admission_inputs WHERE event_id=? AND observed_at=?`, it.Reading.EventID, it.Reading.ObservedAt.Format(time.RFC3339Nano)).Scan(&it.Sequence, &topic, &payload, &digest)
		if errors.Is(e, sql.ErrNoRows) {
			fresh = append(fresh, i)
			continue
		}
		if e != nil {
			return nil, e
		}
		if it.Sequence < 1 {
			return nil, ErrEvidence
		}
		if topic != it.Topic || digest != it.InputSHA256 || !bytes.Equal(payload, it.Payload) {
			return nil, ErrConflict
		}
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM admission_inputs").Scan(&count); e != nil {
		return nil, e
	}
	if count < 0 || count > s.capacity {
		return nil, ErrEvidence
	}
	if len(fresh) > s.capacity-count {
		return nil, ErrCapacity
	}
	for _, i := range fresh {
		it := &items[i]
		result, err := tx.ExecContext(ctx, `INSERT INTO admission_inputs(event_id,observed_at,topic,payload,digest) VALUES(?,?,?,?,?)`, it.Reading.EventID, it.Reading.ObservedAt.Format(time.RFC3339Nano), it.Topic, it.Payload, it.InputSHA256)
		if err != nil {
			return nil, err
		}
		if it.Sequence, e = result.LastInsertId(); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return items, nil
}

// RetainBatch scopes every row before touching the store. It never invokes
// SendOnce. The returned count is all rows or zero, never a confirmed prefix.
func (s *Session) RetainBatch(ctx context.Context, readings []Reading) ([]Item, error) {
	if err := s.begin(); err != nil {
		return nil, err
	}
	defer s.end()
	for _, r := range readings {
		if r.TenantID != s.tenant || r.GatewayID != s.gateway {
			return nil, ErrSignature
		}
	}
	return s.store.PutBatch(ctx, readings)
}
