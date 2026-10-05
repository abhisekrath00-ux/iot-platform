// Command tenantctl is the operator tool for provisioning tenants. It talks to the database directly and has
// no network surface, so creating a tenant is an operator action and never a public sign-up endpoint.
//
//	DATABASE_URL=... tenantctl create --id acme --name "Acme Foods" --admin-email admin@acme.example [--admin-name "Admin"] [--password-stdin]
//	DATABASE_URL=... tenantctl list
//	DATABASE_URL=... tenantctl ai-connect --tenant acme [--base-url http://ai-runtime:8090/v1] [--model qwen3-1.7b]
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
		fmt.Fprintln(os.Stderr, "usage: tenantctl create|list|quota ...")
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
		// A first site so Add device and the commissioning wizard work straight away; rename or add more in the UI.
		if _, err := tx.Exec(ctx, `INSERT INTO sites(id,tenant_id,name) VALUES($1,$2,'Main site') ON CONFLICT DO NOTHING`, *id+"-main", *id); err != nil {
			fatal("could not create the default site: " + err.Error())
		}
		if err := tx.Commit(ctx); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("created tenant %s with admin %s (%s)\n", *id, *email, uid)
	case "quota":
		quotaCmd(ctx, st)
	case "ai-connect":
		// Points a tenant's AI assistant at the bundled local runtime. No key is stored: the runtime is only
		// reachable on the compose network. Operator action, like the rest of tenantctl.
		fs := flag.NewFlagSet("ai-connect", flag.ExitOnError)
		tenant := fs.String("tenant", "", "tenant id")
		base := fs.String("base-url", "http://ai-runtime:8090/v1", "OpenAI-compatible base URL")
		model := fs.String("model", "qwen3-1.7b", "model name")
		fs.Parse(os.Args[2:])
		if !idRe.MatchString(*tenant) || !strings.HasPrefix(*base, "http://ai-runtime:") && !strings.HasPrefix(*base, "http://localhost:") || strings.TrimSpace(*model) == "" || len(*model) > 200 {
			fatal("need --tenant ID, --model NAME and a --base-url on the bundled runtime (http://ai-runtime:PORT/v1)")
		}
		tag, err := st.Pool.Exec(ctx, `INSERT INTO ai_settings(tenant_id,enabled,base_url,model,key_secret,updated_by)
			SELECT id,true,$2,$3,'','tenantctl' FROM tenants WHERE id=$1
			ON CONFLICT (tenant_id) DO UPDATE SET enabled=true, base_url=EXCLUDED.base_url, model=EXCLUDED.model, updated_by='tenantctl', updated_at=now()`, *tenant, *base, *model)
		if err != nil {
			fatal("could not save the AI settings: " + err.Error())
		}
		if tag.RowsAffected() == 0 {
			fatal("no such tenant: " + *tenant)
		}
		fmt.Printf("AI assistant for %s connected to %s (model %s)\n", *tenant, *base, *model)
	default:
		fatal("unknown command " + os.Args[1])
	}
}

func fatal(m string) {
	fmt.Fprintln(os.Stderr, m)
	os.Exit(1)
}

// quota show [--tenant ID|*] and quota set --tenant ID|* [--devices N] [--users N] [--api-keys N] [--customers N].
// N is a whole number, or -1 for unlimited. A flag you leave out keeps its current value. The tenant "*" is
// the default for tenants without their own row. Operator action: a tenant's admin cannot change limits.
func quotaCmd(ctx context.Context, st *store.Store) {
	if len(os.Args) < 3 || (os.Args[2] != "show" && os.Args[2] != "set") {
		fatal("usage: tenantctl quota show [--tenant ID] | quota set --tenant ID|* [--devices N] [--users N] [--api-keys N] [--customers N] (N=-1 unlimited)")
	}
	fs := flag.NewFlagSet("quota", flag.ExitOnError)
	tenant := fs.String("tenant", "", "tenant id, or * for the default")
	vals := map[string]*int{"max_devices": fs.Int("devices", -2, "max devices"), "max_users": fs.Int("users", -2, "max users (including pending invites)"),
		"max_api_keys": fs.Int("api-keys", -2, "max active API keys"), "max_customers": fs.Int("customers", -2, "max customers")}
	fs.Parse(os.Args[3:])
	if os.Args[2] == "show" {
		q, args := `SELECT tenant_id, max_devices, max_users, max_api_keys, max_customers FROM tenant_quotas ORDER BY tenant_id`, []any{}
		if *tenant != "" {
			q, args = `SELECT tenant_id, max_devices, max_users, max_api_keys, max_customers FROM tenant_quotas WHERE tenant_id=$1`, []any{*tenant}
		}
		rows, err := st.Pool.Query(ctx, q, args...)
		if err != nil {
			fatal(err.Error())
		}
		show := func(p *int) string {
			if p == nil {
				return "unlimited"
			}
			return fmt.Sprint(*p)
		}
		for rows.Next() {
			var id string
			var d, u, k, c *int
			rows.Scan(&id, &d, &u, &k, &c)
			fmt.Printf("%-24s devices=%s users=%s api-keys=%s customers=%s\n", id, show(d), show(u), show(k), show(c))
		}
		return
	}
	if *tenant == "" {
		fatal("--tenant is required (a tenant id, or * for the default)")
	}
	if *tenant != "*" {
		var ok bool
		st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1)`, *tenant).Scan(&ok)
		if !ok {
			fatal("no such tenant")
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO tenant_quotas(tenant_id) VALUES($1) ON CONFLICT DO NOTHING`, *tenant); err != nil {
		fatal(err.Error())
	}
	changed := 0
	for col, v := range vals { // col comes from the fixed map above, never from input
		if *v == -2 {
			continue
		}
		if *v < -1 {
			fatal("limits are whole numbers, or -1 for unlimited")
		}
		var arg any
		if *v >= 0 {
			arg = *v
		}
		if _, err := st.Pool.Exec(ctx, `UPDATE tenant_quotas SET `+col+`=$2, updated_at=now() WHERE tenant_id=$1`, *tenant, arg); err != nil {
			fatal(err.Error())
		}
		changed++
	}
	fmt.Printf("updated %d limit(s) for %s\n", changed, *tenant)
}
