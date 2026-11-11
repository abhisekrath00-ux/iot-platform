package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Recovery is an explicit local database-operator action. It neither grants a role
// nor enables a disabled account or removes MFA. The password is never an argument.
func recoverAdmin(ctx context.Context, pool *pgxpool.Pool, workspace, email string, input io.Reader) error {
	if !idRe.MatchString(workspace) || strings.TrimSpace(email) == "" {
		return fmt.Errorf("explicit workspace and email required")
	}
	r := bufio.NewReader(io.LimitReader(input, 1025))
	pw, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("password input requires one newline-terminated line")
	}
	pw = strings.TrimSuffix(pw, "\n")
	pw = strings.TrimSuffix(pw, "\r")
	if rest, _ := io.ReadAll(r); len(rest) != 0 {
		return fmt.Errorf("expected exactly one password line")
	}
	if pw == auth.DefaultAdminPassword {
		return fmt.Errorf("choose a new password, not the first-run credential")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database unavailable")
	}
	defer tx.Rollback(ctx)
	var uid, actualEmail string
	var active bool
	err = tx.QueryRow(ctx, `SELECT id,email,disabled_at IS NULL FROM users WHERE tenant_id=$1 AND lower(email)=lower($2) AND role='admin' FOR UPDATE`, workspace, email).Scan(&uid, &actualEmail, &active)
	if err != nil || !active {
		return fmt.Errorf("active admin not found for this workspace and email; no change made")
	}
	if err = auth.CheckPasswordPolicy(pw, actualEmail); err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("password hashing failed")
	}
	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,failed_logins=0,locked_until=NULL,tokens_valid_after=now() WHERE id=$1`, uid, hash)
	if err != nil || tag.RowsAffected() != 1 {
		return fmt.Errorf("account update failed")
	}
	if _, err = tx.Exec(ctx, `UPDATE password_resets SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, uid); err != nil {
		return fmt.Errorf("reset-token invalidation failed")
	}
	detail, _ := json.Marshal(map[string]string{"method": "local-operator", "scope": "existing-active-admin"})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,'local-operator','user.password_recovered',$2,$3::jsonb)`, workspace, uid, string(detail)); err != nil {
		return fmt.Errorf("audit failed")
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed; verify account state before retrying")
	}
	return nil
}
