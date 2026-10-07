package main

import (
	"context"
	"errors"
	"time"
)

var (
	errActionUnavailable    = errors.New("action state unavailable")
	errActionOutcomeUnknown = errors.New("action outcome unknown; check the target before any new change")
)

// finishAction uses an independent bounded context: closing the browser must not prevent
// recording a returned handler result. If recording fails, do not report success or retry.
func (s *server) finishAction(id, status string, code int, out []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tag, err := s.st.Pool.Exec(ctx, `UPDATE assistant_actions SET status=$2, result_code=$3, result_body=$4
   WHERE id=$1 AND status='executing'`, id, status, code, truncStr(string(out), 4096))
	if err != nil || tag.RowsAffected() != 1 {
		return errActionOutcomeUnknown
	}
	return nil
}

// A 5xx, cancellation during execution or lost result may follow a committed effect.
// No generic endpoint rollback or idempotency is assumed. Unknown is never replayable.
func (s *server) runClaimedAction(ctx context.Context, tenant, user, role, id, method, path, body string) (status string, code int, out []byte, err error) {
	if ctx.Err() != nil {
		status, out = "failed", []byte("cancelled before execution")
		err = s.finishAction(id, status, 0, out)
		return
	}
	code, out = s.loopback(ctx, tenant, user, role, method, path, "", []byte(body))
	status = "executed"
	if code >= 300 {
		status = "failed"
	}
	if code == 0 || code >= 500 || ctx.Err() != nil {
		status = "outcome_unknown"
	}
	if err = s.finishAction(id, status, code, out); err != nil {
		return "outcome_unknown", code, out, err
	}
	if status == "outcome_unknown" {
		err = errActionOutcomeUnknown
	}
	return
}
