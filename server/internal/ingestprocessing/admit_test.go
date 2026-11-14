package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/migrate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/processingschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fixture(t *testing.T) (Engine, Envelope) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("processing_admit_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 8
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a, err := pgxpool.New(c, dsn)
		if err != nil {
			t.Error(err)
			return
		}
		defer a.Close()
		if _, err = a.Exec(c, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	dir := filepath.Join("..", "..", "migrations")
	if err = migrate.Apply(ctx, pool, dir); err != nil {
		t.Fatal(err)
	}
	if _, err = processingschema.Apply(ctx, pool, dir); err != nil {
		t.Fatal(err)
	}
	exec(t, pool, `INSERT INTO tenants(id,name) VALUES('a','Synthetic A'),('b','Synthetic B');
 INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('u','a','processing@example.invalid','Synthetic','admin');
 INSERT INTO sites(id,tenant_id,name) VALUES('s','a','Synthetic');
 INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('g','a','s','SYNTHETIC','active');
 INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES('d','a','g','modbus-generic','Synthetic');
 INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('c','a','email','synthetic@example.invalid',true),('disabled','a','slack','SYNTHETIC',false),('other','b','email','other@example.invalid',true);
 INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('f','a','Synthetic flow','{}','u');
 INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('v','f','a',1,'{"trigger":{"device_id":"d","point_id":"p","op":">","value":1},"steps":[{"type":"notify","channel_id":"c","message":"value {value}"},{"type":"notify","channel_id":"c","message":"again"}]}','published','u');
 UPDATE flows SET published_version_id='v' WHERE id='f'`)
	e := Envelope{EventID: "input", TenantID: "a", GatewayID: "g", DeviceID: "d", PointID: "p", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Quality: "measured", SchemaVersion: 1}
	return Engine{Pool: pool, Enabled: true}, e
}
func exec(t *testing.T, p *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := p.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func counts(t *testing.T, p *pgxpool.Pool, event string, want int) {
	t.Helper()
	for _, table := range []string{"telemetry", "ingest_processing_v1"} {
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE event_id=$1", event).Scan(&n); err != nil || n != want {
			t.Fatal(table, n, want, err)
		}
	}
}
func TestDisabledAndCanonical(t *testing.T) {
	if _, err := (Engine{}).Admit(t.Context(), Envelope{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	base := Envelope{EventID: "e", TenantID: "t", GatewayID: "g", DeviceID: "d", PointID: "p", ObservedAt: time.Unix(1730000000, 123456789), Quality: "estimated", SchemaVersion: 1}
	c, b, err := canonical(base)
	if err != nil || c.ObservedAt.Nanosecond() != 123456000 {
		t.Fatal(c, err)
	}
	if !bytes.Contains(b, []byte(`"estimated"`)) {
		t.Fatal(string(b))
	}
	for _, change := range []func(*Envelope){func(e *Envelope) { e.ObservedAt = time.Time{} }, func(e *Envelope) { e.EventID = strings.Repeat("a", 257) }, func(e *Envelope) { e.Quality = "invented" }, func(e *Envelope) { e.Unit = strings.Repeat("x", 65) }} {
		e := base
		change(&e)
		if _, _, err := canonical(e); !errors.Is(err, ErrInput) {
			t.Fatal(e, err)
		}
	}
}
func TestAtomicAdmissionReplayAndQuarantine(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	got, err := x.Admit(ctx, e)
	if err != nil || !got.New || got.State != "pending" || len(got.Plan.Intents) != 2 || len(got.Plan.Blocked) != 0 {
		t.Fatal(got, err)
	}
	if len(got.Plan.Channels) != 2 || got.Plan.Intents[0].Target != "synthetic@example.invalid" || got.Plan.Intents[1].Sequence != 1 {
		t.Fatal(got.Plan)
	}
	first, _ := json.Marshal(got.Plan)
	counts(t, x.Pool, e.EventID, 1)
	exec(t, x.Pool, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='c';UPDATE flow_versions SET definition='[]' WHERE id='v';UPDATE ingest_processing_v1 SET state='outcome_unknown',attempt_id='11111111-1111-4111-8111-111111111111' WHERE event_id='input'`)
	replay, err := x.Admit(ctx, e)
	again, _ := json.Marshal(replay.Plan)
	if err != nil || replay.New || replay.State != "outcome_unknown" || !bytes.Equal(first, again) {
		t.Fatal(replay, err)
	}
	for _, change := range []func(*Envelope){func(e *Envelope) { e.Value++ }, func(e *Envelope) { e.TenantID = "b" }, func(e *Envelope) { e.GatewayID = "else" }} {
		v := e
		change(&v)
		if _, err = x.Admit(ctx, v); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	e.EventID = "after-edit"
	blocked, err := x.Admit(ctx, e)
	if err != nil || !blocked.New || len(blocked.Plan.Blocked) != 1 || len(blocked.Plan.Intents) != 0 {
		t.Fatal(blocked, err)
	}
	for _, table := range []string{"alerts", "flow_runs", "commands"} {
		var n int
		if err = x.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	// Neither a checksum nor a replay grants execution. Corrupt derived intent
	// with a recomputed checksum still fails the retained-config derivation.
	var raw []byte
	if err = x.Pool.QueryRow(ctx, `SELECT plan FROM ingest_processing_v1 WHERE event_id='input'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p Plan
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p.Intents[0].Target = "injected@example.invalid"
	raw, _ = json.Marshal(p)
	sum := sha256.Sum256(raw)
	exec(t, x.Pool, `UPDATE ingest_processing_v1 SET plan=$1,plan_sha256=$2 WHERE event_id='input'`, raw, sum[:])
	e.EventID = "input"
	if _, err = x.Admit(ctx, e); !errors.Is(err, ErrPlan) {
		t.Fatal(err)
	}
}
func TestAdmissionAllOrNothingAndLegacy(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	exec(t, x.Pool, `CREATE FUNCTION reject_processing() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic processing insert fault';END$$;CREATE TRIGGER reject_processing BEFORE INSERT ON ingest_processing_v1 FOR EACH ROW EXECUTE FUNCTION reject_processing()`)
	if _, err := x.Admit(ctx, e); err == nil {
		t.Fatal("insert fault accepted")
	}
	counts(t, x.Pool, e.EventID, 0)
	exec(t, x.Pool, `DROP TRIGGER reject_processing ON ingest_processing_v1;DROP FUNCTION reject_processing()`)
	exec(t, x.Pool, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES($1,'a','g','d','p',$2,42,'','measured',1)`, e.EventID, e.ObservedAt)
	if _, err := x.Admit(ctx, e); !errors.Is(err, ErrLegacy) {
		t.Fatal(err)
	}
	var n int
	if err := x.Pool.QueryRow(ctx, "SELECT count(*) FROM ingest_processing_v1").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	e.EventID = "wrong-device-binding"
	e.GatewayID = "wrong"
	if _, err := x.Admit(ctx, e); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 0)
}
func TestAdmissionConcurrentExactReplay(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	results := make(chan error, 12)
	news := make(chan bool, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); a, err := x.Admit(ctx, e); results <- err; news <- a.New }()
	}
	wg.Wait()
	close(results)
	close(news)
	for err := range results {
		if err != nil {
			var pe interface{ SQLState() string }
			if !errors.As(err, &pe) || pe.SQLState() != "40001" {
				t.Fatal(err)
			}
		}
	}
	fresh := 0
	for n := range news {
		if n {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatal(fresh)
	}
	if a, err := x.Admit(ctx, e); err != nil || a.New {
		t.Fatal(a, err)
	}
	counts(t, x.Pool, e.EventID, 1)
}
func TestAdmissionBoundedAndUnsupported(t *testing.T) {
	for _, mode := range []string{"oversize-definition", "too-many-channels", "plan-byte-limit", "cross-tenant-version", "http-stage", "scoped-rule", "position-stage"} {
		t.Run(mode, func(t *testing.T) {
			x, e := fixture(t)
			switch mode {
			case "oversize-definition":
				exec(t, x.Pool, `UPDATE flow_versions SET definition=jsonb_build_object('huge',repeat('x',65537)) WHERE id='v'`)
			case "too-many-channels":
				exec(t, x.Pool, `INSERT INTO notification_channels(id,tenant_id,type,target) SELECT 'extra'||i,'a','email','synthetic@example.invalid' FROM generate_series(1,127)i`)
			case "plan-byte-limit":
				exec(t, x.Pool, `INSERT INTO notification_channels(id,tenant_id,type,target) SELECT 'extra'||i,'a','slack',repeat('x',8192) FROM generate_series(1,40)i`)
			case "cross-tenant-version":
				exec(t, x.Pool, `UPDATE flow_versions SET tenant_id='b' WHERE id='v'`)
			case "http-stage":
				exec(t, x.Pool, `UPDATE flow_versions SET definition='{"graph":{"nodes":[{"id":"t","type":"trigger","device_id":"d","point_id":"p","op":">","value":1},{"id":"h","type":"http","method":"GET","url":"https://synthetic.invalid/never-called","target":"response"},{"id":"n","type":"notify","channel_id":"c"}],"edges":[{"from":"t","to":"h"},{"from":"h","to":"n"}]}}' WHERE id='v'`)
			case "scoped-rule":
				exec(t, x.Pool, `INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES('r','a','Synthetic','{"group_id":"synthetic-group","point_id":"p","op":">","threshold":0,"severity":"warning"}',true,'u')`)
			case "position-stage":
				exec(t, x.Pool, `UPDATE devices SET lat_point='p',lon_point='lon' WHERE id='d'`)
			}
			a, err := x.Admit(t.Context(), e)
			if mode == "oversize-definition" || mode == "too-many-channels" || mode == "plan-byte-limit" {
				if !errors.Is(err, ErrPlan) {
					t.Fatal(a, err)
				}
				counts(t, x.Pool, e.EventID, 0)
			} else {
				if err != nil || len(a.Plan.Blocked) == 0 {
					t.Fatal(a, err)
				}
				counts(t, x.Pool, e.EventID, 1)
				if mode == "http-stage" && !strings.Contains(strings.Join(a.Plan.Blocked, " "), "node_stage_required:http") {
					t.Fatal("HTTP guard was not reached", a.Plan.Blocked)
				}
			}
		})
	}
}
func TestSelectionSingleSnapshot(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	tx, err := x.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	// Establish snapshot, then atomically change related flow/channel policy on
	// another connection. Capture cannot splice old flow with new channel.
	var old string
	if err = tx.QueryRow(ctx, `SELECT target FROM notification_channels WHERE id='c'`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, `BEGIN;UPDATE notification_channels SET target='new@example.invalid' WHERE id='c';UPDATE flow_versions SET definition=jsonb_set(definition,'{steps,0,message}','"new message"') WHERE id='v';COMMIT`)
	p, err := capture(ctx, tx, e)
	if err != nil || p.Intents[0].Target != old || p.Intents[0].Message != "value 42" {
		t.Fatal(p, err)
	}
	rollback(tx)
	a, err := x.Admit(ctx, e)
	if err != nil || a.Plan.Intents[0].Target != "new@example.invalid" || a.Plan.Intents[0].Message != "new message" {
		t.Fatal(a, err)
	}
}
func TestReadWhileDisabledAndCancellation(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	exec(t, x.Pool, `UPDATE devices SET lat_point='p' WHERE id='d'`)
	a, err := x.Admit(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	x.Enabled = false
	r, err := x.Read(ctx, e.TenantID, e.EventID, e.ObservedAt)
	if err != nil || r.State != "pending" || len(r.Plan.Blocked) != 1 || r.Plan.Intents[0] != a.Plan.Intents[0] {
		t.Fatal(r, err)
	}
	if _, err = x.Read(ctx, "b", e.EventID, e.ObservedAt); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	x.Enabled = true
	c, cancel := context.WithCancel(ctx)
	cancel()
	e.EventID = "cancelled"
	if _, err = x.Admit(c, e); err == nil {
		t.Fatal("cancelled admission accepted")
	}
	counts(t, x.Pool, e.EventID, 0)
}
func TestRetainedCapacityRefusesButAllowsExactReplay(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	if _, err := x.Admit(ctx, e); err != nil {
		t.Fatal(err)
	}
	// Synthetic bounded filler is deliberately not decodable work. Capacity is
	// a retained-row limit, including completed/unknown, not only pending jobs.
	exec(t, x.Pool, `INSERT INTO ingest_processing_v1(event_id,observed_at,tenant_id,envelope,plan,plan_sha256,policy,state,attempt_id)
 SELECT 'capacity-'||i,now(),'a','x'::bytea,'x'::bytea,decode(repeat('00',32),'hex'),'processing-v1','completed','11111111-1111-4111-8111-111111111111' FROM generate_series(1,99999)i`)
	if a, err := x.Admit(ctx, e); err != nil || a.New {
		t.Fatal(a, err)
	}
	e.EventID = "beyond-capacity"
	if _, err := x.Admit(ctx, e); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 0)
}
func TestEverySideEffectNodeClassifiedBeforeExecution(t *testing.T) {
	for _, n := range []flow.Node{
		{Type: "http", Method: "GET", URL: "https://synthetic.invalid/never-called", Target: "response"},
		{Type: "context", Mode: "incr", Scope: "global", Key: "counter", Value: 1},
		{Type: "control", TargetID: "synthetic-target", Value: 1},
		{Type: "function", Code: "return msg;"},
		{Type: "rate_limit", Seconds: 60},
	} {
		t.Run(n.Type, func(t *testing.T) {
			n.ID = "unsafe"
			d := flow.Definition{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "t", Type: "trigger", DeviceID: "d", PointID: "p", Op: ">", Value: 1}, n, {ID: "n", Type: "notify", ChannelID: "c"}}, Edges: []flow.Edge{{From: "t", To: "unsafe"}, {From: "unsafe", To: "n"}}}}
			if err := flow.Validate(d); err != nil {
				t.Fatal("invalid guard fixture", err)
			}
			b, _ := json.Marshal(d)
			p := Plan{Envelope: Envelope{DeviceID: "d", PointID: "p", Value: 42}, Channels: []Channel{{ID: "c", Type: "email", Target: "synthetic@example.invalid", Enabled: true}}, Flows: []Flow{{ID: "f", VersionID: "v", Version: 1, Definition: b}}}
			if err := derive(&p); err != nil || len(p.Intents) != 0 || len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "node_stage_required:"+n.Type) {
				t.Fatal(p, err)
			}
		})
	}
}
