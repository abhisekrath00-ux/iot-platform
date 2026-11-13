package setupconnection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestExplicitBoundedRequest(t *testing.T) {
	valid := `{"version":1,"transport":"private_tcp_plain","host":"127.0.0.1","port":5432,"database":"app","user":"admin","password":"private-not-output"}`
	if _, err := Read(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, valid + `{}`, strings.Replace(valid, `"port":5432`, `"port":5432,"port":5433`, 1), strings.Replace(valid, `"host":"127.0.0.1"`, `"host":"postgres"`, 1), strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"port":5432`, `"port":0`, 1), strings.Replace(valid, `"password":"private-not-output"`, `"password":"x","approval":true`, 1), strings.Replace(valid, `"host":"127.0.0.1"`, `"host":"8.8.8.8"`, 1), strings.Replace(valid, `"host":"127.0.0.1"`, `"host":"0.0.0.0"`, 1), strings.Replace(valid, `"transport":"private_tcp_plain"`, `"transport":"tls"`, 1), strings.Repeat("x", 8193)} {
		if _, err := Read(strings.NewReader(body)); err == nil {
			t.Fatalf("invalid request accepted")
		}
	}
	t.Setenv("PGSERVICE", "unrelated")
	_, err := Probe(context.Background(), Request{Version: 1, Transport: "private_tcp_plain", Host: "127.0.0.1", Port: 1, Database: "a", User: "b"})
	if err == nil || strings.Contains(err.Error(), "unrelated") {
		t.Fatal("ambient settings not safely refused")
	}
}

func TestExplicitLocalSocketRequest(t *testing.T) {
	valid := `{"version":1,"transport":"local_socket","host":"/var/run/postgresql","port":5432,"database":"app","user":"admin","password":""}`
	if _, err := Read(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"/tmp/../run", "/tmp/", "/tmp//run", "relative", "@abstract", "/tmp,/run", "127.0.0.1"} {
		if _, err := Read(strings.NewReader(strings.Replace(valid, "/var/run/postgresql", host, 1))); err == nil {
			t.Fatal("noncanonical socket accepted")
		}
	}
	if _, err := Read(strings.NewReader(strings.Replace(valid, `"password":""`, `"password":"secret"`, 1))); err == nil {
		t.Fatal("socket password accepted")
	}
}

func TestActualPostgresConnectedDeniedClosedAndNoMutation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("explicit disposable database required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("hx_probe_%d", time.Now().UnixNano())
	role := name + "_role"
	pass := "synthetic-fixture-only"
	for _, sql := range []string{"CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " LOGIN PASSWORD '" + pass + "'", "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()} {
		if _, err = admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	defer admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
	defer admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	cfg := admin.Config()
	r := Request{Version: 1, Transport: "private_tcp_plain", Host: cfg.Host, Port: cfg.Port, Database: name, User: role, Password: pass}
	value, err := Probe(ctx, r)
	if err != nil || value.Status != "connected" || value.Database != name || value.User != role || value.Server != "127.0.0.1" || value.Client != "127.0.0.1" {
		t.Fatalf("connection proof: %+v %v", value, err)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), pass) {
		t.Fatal("password leaked")
	}
	check, err := pgx.Connect(ctx, strings.Replace(dsn, admin.Config().Database, name, 1))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = check.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname='public'").Scan(&count); err != nil || count != 0 {
		t.Fatal("probe changed application rows/schema")
	}
	check.Close(ctx)
	var sockets string
	if err = admin.QueryRow(ctx, "SHOW unix_socket_directories").Scan(&sockets); err != nil {
		t.Fatal(err)
	}
	local := Request{Version: 1, Transport: "local_socket", Host: sockets, Port: cfg.Port, Database: name, User: cfg.User}
	value, err = Probe(ctx, local)
	if err != nil || value.Status != "connected" || value.Server != "" || value.Client != "" {
		t.Fatalf("local proof: %+v %v", value, err)
	}
	if _, err = admin.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	value, err = Probe(ctx, r)
	if err != nil || value.Status != "database_closed" {
		t.Fatalf("closed proof: %+v %v", value, err)
	}
	value, err = Probe(ctx, local)
	if err != nil || value.Status != "database_closed" {
		t.Fatalf("local closed proof: %+v %v", value, err)
	}
}
