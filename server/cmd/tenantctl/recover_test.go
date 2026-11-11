package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecoveryExistingAdmin(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	admin := p
	// Dedicated schema models only the columns used by this tool, not product migrations.
	schema := "recover_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, e = p.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	c, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	c.ConnConfig.RuntimeParams["search_path"] = schema
	p, e = pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	_, e = p.Exec(ctx, `CREATE TABLE users(id text PRIMARY KEY,tenant_id text,email text,role text,disabled_at timestamptz,password_hash text,failed_logins int,locked_until timestamptz,tokens_valid_after timestamptz,must_change_credentials bool,mfa_marker text);CREATE TABLE password_resets(user_id text,used_at timestamptz);CREATE TABLE audit_log(tenant_id text,actor text,action text,target text,detail jsonb);CREATE TABLE keep_data(value text);INSERT INTO keep_data VALUES('preserve');INSERT INTO users VALUES('u','hexthings','fixture@example.invalid','admin',NULL,'old',4,now()+interval '1 day',NULL,true,'retain-mfa');INSERT INTO password_resets VALUES('u',NULL)`)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct{ workspace, email, pw string }{{"wrong", "fixture@example.invalid", "new-fixture-password\n"}, {"hexthings", "wrong@example.invalid", "new-fixture-password\n"}, {"hexthings", "fixture@example.invalid", "short\n"}, {"hexthings", "fixture@example.invalid", "new-fixture-password"}, {"hexthings", "fixture@example.invalid", "new-fixture-password\nextra\n"}} {
		if recoverAdmin(ctx, p, x.workspace, x.email, strings.NewReader(x.pw)) == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, e = p.Exec(ctx, `UPDATE users SET disabled_at=now()`); e != nil {
		t.Fatal(e)
	}
	if recoverAdmin(ctx, p, "hexthings", "fixture@example.invalid", strings.NewReader("new-fixture-password\n")) == nil {
		t.Fatal("disabled admin accepted")
	}
	p.Exec(ctx, `UPDATE users SET disabled_at=NULL,role='viewer'`)
	if recoverAdmin(ctx, p, "hexthings", "fixture@example.invalid", strings.NewReader("new-fixture-password\n")) == nil {
		t.Fatal("nonadmin accepted")
	}
	p.Exec(ctx, `UPDATE users SET role='admin'`)
	if e = recoverAdmin(ctx, p, "hexthings", "fixture@example.invalid", strings.NewReader("new-fixture-password\n")); e != nil {
		t.Fatal(e)
	}
	var hash, mfa, kept string
	var failed int
	var valid *time.Time
	var must bool
	e = p.QueryRow(ctx, `SELECT password_hash,failed_logins,tokens_valid_after,must_change_credentials,mfa_marker FROM users WHERE id='u'`).Scan(&hash, &failed, &valid, &must, &mfa)
	if e != nil || !auth.VerifyPassword("new-fixture-password", hash) || failed != 0 || valid == nil || !must || mfa != "retain-mfa" {
		t.Fatal("account state incorrect", e)
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM password_resets WHERE used_at IS NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("reset token still live")
	}
	p.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='user.password_recovered'`).Scan(&n)
	if n != 1 {
		t.Fatal("audit missing")
	}
	p.QueryRow(ctx, `SELECT value FROM keep_data`).Scan(&kept)
	if kept != "preserve" {
		t.Fatal("data changed")
	}
	// Audit failure must roll the password change back.
	p.Exec(ctx, `DROP TABLE audit_log`)
	if recoverAdmin(ctx, p, "hexthings", "fixture@example.invalid", strings.NewReader("other-fixture-password\n")) == nil {
		t.Fatal("audit failure accepted")
	}
	var after string
	p.QueryRow(ctx, `SELECT password_hash FROM users WHERE id='u'`).Scan(&after)
	if after != hash {
		t.Fatal("failed transaction changed password")
	}
}
