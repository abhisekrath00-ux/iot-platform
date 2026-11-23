// Package applyadmission holds an isolated Apply safety candidate. It never
// installs SQL. Callers must supply an already guarded, disposable acceptance
// database until separate adoption and recovery approval exists.
package applyadmission

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed candidate.sql
var candidate string

// Verify checks actual definitions, not the migration count or object names.
// The transaction's table lock prevents concurrent table/trigger DDL until the
// caller commits its unresolved attempt. This is not an administrator boundary
// or proof that a restored database retained post-snapshot external effects.
func Verify(ctx context.Context, tx pgx.Tx) error {
	var replicationRole string
	if err := tx.QueryRow(ctx, "SHOW session_replication_role").Scan(&replicationRole); err != nil || replicationRole != "origin" {
		return fmt.Errorf("Apply requires normal trigger execution")
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE gateway_config_pushes IN ROW EXCLUSIVE MODE"); err != nil {
		return err
	}
	var body, predicate string
	var trigger, index bool
	err := tx.QueryRow(ctx, `SELECT p.prosrc,
 EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid='gateway_config_pushes'::regclass
 AND t.tgname='apply_attempt_guard_candidate' AND t.tgfoid=p.oid AND t.tgenabled='O'
 AND t.tgtype=31 AND t.tgnargs=0 AND t.tgqual IS NULL AND NOT t.tgisinternal),
 EXISTS(SELECT 1 FROM pg_index i WHERE i.indexrelid=to_regclass('apply_open_attempt_candidate')
 AND i.indrelid='gateway_config_pushes'::regclass AND i.indisunique AND i.indisvalid
 AND i.indisready AND i.indnkeyatts=2 AND i.indnatts=2
 AND pg_get_indexdef(i.indexrelid,1,true)='tenant_id'
 AND pg_get_indexdef(i.indexrelid,2,true)='gateway_id'),
 COALESCE((SELECT pg_get_expr(i.indpred,i.indrelid) FROM pg_index i WHERE i.indexrelid=to_regclass('apply_open_attempt_candidate')),'')
 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid=to_regprocedure('apply_attempt_guard_candidate()') AND l.lanname='plpgsql'
 AND NOT p.prosecdef AND p.prorettype='trigger'::regtype AND p.proconfig IS NULL`).Scan(&body, &trigger, &index, &predicate)
	if err != nil {
		return fmt.Errorf("Apply safety guard unavailable: %w", err)
	}
	parts := strings.Split(candidate, "AS $guard$")
	if len(parts) != 2 {
		return fmt.Errorf("invalid embedded Apply guard")
	}
	expected := strings.Split(parts[1], "$guard$")[0]
	if strings.TrimSpace(body) != strings.TrimSpace(expected) || !trigger || !index || predicate != "(status = ANY (ARRAY['sent'::text, 'applied'::text]))" {
		return fmt.Errorf("Apply safety guard differs from reviewed definitions")
	}
	return nil
}
