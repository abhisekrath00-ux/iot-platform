package setupbootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Synthetic test-binary operator only, never built into tenantctl or an owner
// command. The native harness owns a fresh temporary Docker project/database.
func TestSyntheticApplicationFixtureOperator(t *testing.T) {
	action := os.Getenv("HEXTHINGS_SYNTHETIC_APP_ACTION")
	if action == "" {
		t.Skip("disposable native application harness only")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	parsed, e := url.Parse(dsn)
	if e != nil || os.Getenv("GITHUB_ACTIONS") != "true" || parsed.Hostname() != "postgres" || parsed.Path != "/application_fixture" || parsed.User == nil || parsed.User.Username() != "ci" {
		t.Fatal("only dedicated synthetic native application database allowed")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	switch action {
	case "seed89":
		var exists *string
		if e = p.QueryRow(ctx, "SELECT to_regclass('schema_migrations')::text").Scan(&exists); e != nil || exists != nil {
			t.Fatal("fresh synthetic database required")
		}
		applyBaseline(t, p)
		if _, e = p.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role)VALUES('retain-user','retain-tenant','synthetic@example.invalid','Synthetic','admin')`); e != nil {
			t.Fatal(e)
		}
		if _, e = p.Exec(ctx, `UPDATE device_profiles SET points='[{"id":"voltage","address":42,"type":"u16","scale":1,"min":0,"max":500}]' WHERE id='retain-profile';UPDATE devices SET config='{"port":"unchanged","slave_id":11,"device_profile_id":"retain-profile","connection":{"port":"COM7","slave_id":11,"baud":9600,"parity":"E","stop_bits":1}}' WHERE id='retain-device'`); e != nil {
			t.Fatal(e)
		}
	case "seed-writes":
		// Dedicated synthetic records keep mutation probes away from retained
		// owner-like source/identity snapshots. Never a production command.
		if _, e = p.Exec(ctx, `INSERT INTO tenants(id,name)VALUES('write-tenant','Synthetic compatibility');
INSERT INTO users(id,tenant_id,email,display_name,role)VALUES('write-user','write-tenant','writes@example.invalid','Synthetic','admin'),('write-viewer','write-tenant','viewer@example.invalid','Synthetic viewer','viewer');
INSERT INTO sites(id,tenant_id,name)VALUES('write-site','write-tenant','Synthetic');
INSERT INTO gateways(id,tenant_id,site_id,serial,status)VALUES('write-gw','write-tenant','write-site','SYNTHETIC-WRITES','active');
INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config)VALUES('write-device','write-tenant','write-gw','modbus-generic','Synthetic writes','{}');
INSERT INTO points(id,device_id,unit,min_value,max_value)VALUES('write-voltage','write-device','V',0,500)`); e != nil {
			t.Fatal(e)
		}
	case "apply":
		if _, e = ApplyReviewed(ctx, p, sourceDir()); e != nil {
			t.Fatal(e)
		}
	case "inspect":
		value, e := InspectReviewed(ctx, p, sourceDir())
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		fmt.Println("HEXTHINGS_SCHEMA=" + string(raw))
	case "rows":
		assertRows(t, p, 93)
		for _, check := range []struct{ query, want string }{
			{`SELECT points::text FROM device_profiles WHERE id='retain-profile'`, `[{"id":"voltage","address":42,"type":"u16","scale":1,"min":0,"max":500}]`},
			{`SELECT config::text FROM devices WHERE id='retain-device'`, `{"port":"unchanged","slave_id":11,"device_profile_id":"retain-profile","connection":{"port":"COM7","slave_id":11,"baud":9600,"parity":"E","stop_bits":1}}`},
		} {
			var raw string
			if e = p.QueryRow(ctx, check.query).Scan(&raw); e != nil {
				t.Fatal(e)
			}
			var actual, want any
			if json.Unmarshal([]byte(raw), &actual) != nil || json.Unmarshal([]byte(check.want), &want) != nil || !reflect.DeepEqual(actual, want) {
				t.Fatal("synthetic source/profile configuration changed")
			}
		}

	default:
		t.Fatal("unknown synthetic action")
	}
}
