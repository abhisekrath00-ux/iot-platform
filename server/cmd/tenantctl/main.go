// Command tenantctl is the operator tool for provisioning tenants. It talks to the database directly and has
// no network surface, so creating a tenant is an operator action and never a public sign-up endpoint.
//
//	DATABASE_URL=... tenantctl create --id acme --name "Acme Foods" --admin-email admin@acme.example [--admin-name "Admin"] [--password-stdin]
//	DATABASE_URL=... tenantctl list
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tenantctl create|list ...")
		os.Exit(2)
	}
	ctx := context.Background()
	st, err := store.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "database:", err)
		os.Exit(1)
	}
	defer st.Close()
	switch os.Args[1] {
	case "list":
		rows, err := st.Pool.Query(ctx, `SELECT t.id, t.name, (SELECT count(*) FROM users u WHERE u.tenant_id=t.id), (SELECT count(*) FROM devices d WHERE d.tenant_id=t.id) FROM tenants t ORDER BY t.id`)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for rows.Next() {
			var id, name string
			var u, d int
			rows.Scan(&id, &name, &u, &d)
			fmt.Printf("%-24s %-32s users=%d devices=%d\n", id, name, u, d)
		}
	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		id := fs.String("id", "", "tenant id: lowercase letters, digits, dashes (3-40)")
		name := fs.String("name", "", "display name")
		email := fs.String("admin-email", "", "first admin's email")
		aname := fs.String("admin-name", "Administrator", "first admin's display name")
		pwStdin := fs.Bool("password-stdin", false, "read the admin's initial password from stdin (needs LOCAL_LOGIN deployments; omit for SSO-only)")
		fs.Parse(os.Args[2:])
		if !idRe.MatchString(*id) || strings.TrimSpace(*name) == "" || len(*name) > 128 {
			fatal("need --id (3-40 chars a-z 0-9 -) and --name")
		}
		if a, err := mail.ParseAddress(*email); err != nil || a.Address != *email {
			fatal("need a valid --admin-email")
		}
		var hash *string
		if *pwStdin {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			pw := strings.TrimRight(line, "\r\n")
			if err := auth.CheckPasswordPolicy(pw, *email); err != nil {
				fatal(err.Error())
			}
			h, err := auth.HashPassword(pw)
			if err != nil {
				fatal(err.Error())
			}
			hash = &h
		}
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			fatal(err.Error())
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,$2)`, *id, *name); err != nil {
			fatal("tenant exists or insert failed: " + err.Error())
		}
		uid := "u-" + *id + "-admin"
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash) VALUES($1,$2,$3,$4,'admin',$5)`, uid, *id, *email, *aname, hash); err != nil {
			fatal("admin email already registered or insert failed: " + err.Error())
		}
		if err := tx.Commit(ctx); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("created tenant %s with admin %s (%s)\n", *id, *email, uid)
	default:
		fatal("unknown command " + os.Args[1])
	}
}

func fatal(m string) {
	fmt.Fprintln(os.Stderr, m)
	os.Exit(1)
}
