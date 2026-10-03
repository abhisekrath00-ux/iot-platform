package main

import (
	"context"
	"log"
)

// sweepAutoCommands publishes commands that the flow engine raised under an admin's "automatic"
// setting on an alarm-output target, and expires the ones nobody dispatched in time. It handles ONLY
// auto_approved rows: a command a person approved is dispatched by the approve call itself.
func (s *server) sweepAutoCommands(ctx context.Context) {
	if tag, err := s.st.Pool.Exec(ctx,
		`UPDATE commands SET status='expired' WHERE auto_approved AND status='approved' AND expires_at <= now()`); err == nil && tag.RowsAffected() > 0 {
		log.Printf("auto-commands: %d expired before dispatch", tag.RowsAffected())
	}
	rows, err := s.st.Pool.Query(ctx,
		`SELECT tenant_id, request_id FROM commands WHERE auto_approved AND status='approved' AND expires_at > now() ORDER BY created_at LIMIT 20`)
	if err != nil {
		return
	}
	type job struct{ tenant, id string }
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.tenant, &j.id) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		next, gw := s.dispatchCore(ctx, j.tenant, j.id)
		s.st.Pool.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,'system:auto-dispatch',$2,$3,$4)`,
			j.tenant, "command."+next, j.id, `{"automatic":true,"topic_gateway":"`+gw+`"}`)
	}
}
