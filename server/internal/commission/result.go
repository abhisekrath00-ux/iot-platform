package commission

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ProbeResult is evidence from a gateway, not an external action instruction.
type ProbeResult struct {
	SessionID  string          `json:"session_id"`
	RequestID  string          `json:"request_id,omitempty"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error"`
	Readings   json.RawMessage `json:"readings"`
	LatencyMs  int64           `json:"latency_ms"`
	FinishedAt time.Time       `json:"finished_at"`
}

// RecordResult fences delayed/out-of-order replies by the exact stored request.
// Legacy empty IDs only match legacy empty-ID requests. They can never settle a
// new range preview, and missing IDs from an old edge fail closed after upgrade.
func RecordResult(ctx context.Context, pool *pgxpool.Pool, tenant, gateway string, res ProbeResult) (int64, error) {
	payload, err := json.Marshal(res)
	if err != nil {
		return 0, err
	}
	tag, err := pool.Exec(ctx, `UPDATE commissioning_sessions
 SET port_test=COALESCE(port_test,'{}'::jsonb)-'result'||jsonb_build_object('result',$1::jsonb),
 state=CASE WHEN $2 THEN 'tested' ELSE 'failed' END,updated_at=now()
 WHERE id=$3 AND tenant_id=$4 AND gateway_id=$5
 AND COALESCE(port_test->'config'->>'request_id','')=$6`, payload, res.OK, res.SessionID, tenant, gateway, res.RequestID)
	return tag.RowsAffected(), err
}
