package admissionoutbox

import (
	"context"
	"time"
)

// RecoveryStatus is a read-only observation, not a delivery receipt. Collection
// and session are sampled separately, not an atomic snapshot. Counts can change
// immediately. AsOf is gateway observation time, not a meter clock. LastSend is
// process-local and means signed admission only, never provider completion.
// No retained payload, source address, endpoint, private path or key is exposed.
type RecoveryStatus struct {
	AsOf       time.Time       `json:"as_of"`
	Collection CollectorStatus `json:"collection"`
	Session    SessionStatus   `json:"session"`
}

// RecoveryStatus performs no poll, retain, send, reconciliation or Close.
// Any session status failure returns zero evidence rather than a stale count.
func (c *Collector) RecoveryStatus(ctx context.Context) (RecoveryStatus, error) {
	if c == nil || c.session == nil {
		return RecoveryStatus{}, ErrSessionClosed
	}
	if ctx == nil {
		return RecoveryStatus{}, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return RecoveryStatus{}, err
	}
	collection := c.Status()
	session, err := c.session.Status(ctx)
	if err != nil {
		return RecoveryStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return RecoveryStatus{}, err
	}
	return RecoveryStatus{AsOf: time.Now().UTC(), Collection: collection, Session: session}, nil
}
