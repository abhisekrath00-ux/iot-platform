package main

import (
	"context"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

// Publish changes configuration only. No evaluator or delivery client is run.
func TestIntegrationFlowPublishStoredValidation(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-flow-publish-guard"
	seed(t, s, tenant)
	id := tenant + "-flow"
	cleanup := func(c context.Context) {
		for _, q := range []string{`DROP TRIGGER IF EXISTS itest_publish_refuse ON flows`, `DROP FUNCTION IF EXISTS itest_publish_refuse()`, `UPDATE flows SET published_version_id=NULL WHERE id=$1`, `DELETE FROM flows WHERE id=$1`} {
			var err error
			if q[0:4] == "DROP" {
				_, err = s.st.Pool.Exec(c, q)
			} else {
				_, err = s.st.Pool.Exec(c, q, id)
			}
			if err != nil {
				t.Error(err)
			}
		}
	}
	cleanup(ctx)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanup(c)
	})
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,'publish fixture','{}','test-user')`, id, tenant); err != nil {
		t.Fatal(err)
	}
	def := `{"trigger":{"device_id":"fixture","point_id":"temp","op":">","value":0},"steps":[{"type":"notify","channel_id":"absent-fixture-channel","message":"synthetic"}]}`
	for v := 1; v <= 3; v++ {
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES($1,$2,$3,$4,$5,'draft','test-user')`, fmt.Sprintf("%s-v%d", id, v), id, tenant, v, def); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET status='published' WHERE id=$1`, id+"-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=$2 WHERE id=$1`, id, id+"-v1"); err != nil {
		t.Fatal(err)
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/flows/{id}/publish", s.publishFlow)
	api.HandleFunc("POST /v1/flows/{id}/rollback", s.rollbackFlow)
	check := func(want string) {
		t.Helper()
		var pointer string
		var n int
		if err := s.st.Pool.QueryRow(ctx, `SELECT published_version_id,(SELECT count(*) FROM flow_versions WHERE flow_id=$1 AND status='published') FROM flows WHERE id=$1`, id).Scan(&pointer, &n); err != nil || pointer != want || n != 1 {
			t.Fatalf("pointer=%s published=%d want=%s err=%v", pointer, n, want, err)
		}
	}
	// JSONB may hold a syntactically valid value that is not a valid definition.
	for _, bad := range []string{`[]`, `{}`, `{"trigger":{"device_id":"fixture","point_id":"temp","op":">","value":0},"steps":[{"type":"unknown"}]}`} {
		if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET definition=$2 WHERE id=$1`, id+"-v2", bad); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"publish", "rollback"} {
			w := call(api, tenant, "operator", "POST", "/v1/flows/"+id+"/"+action, `{"version":2}`)
			if w.Code != 400 {
				t.Errorf("invalid stored definition %s: code=%d body=%s", action, w.Code, w.Body.String())
			}
			check(id + "-v1")
		}
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET definition=$2 WHERE id=$1`, id+"-v2", def); err != nil {
		t.Fatal(err)
	}
	// Publishing feature-bearing definitions must use the same transaction's
	// connection. A one-connection pool proves no nested pool wait is needed.
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	limited := &server{st: &store.Store{Pool: one}}
	limitedAPI := http.NewServeMux()
	limitedAPI.HandleFunc("POST /v1/flows/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		limited.publishFlow(w, r.WithContext(c))
	})
	for _, feature := range []struct{ name, node string }{
		{"function_nodes", `{"id":"x","type":"function","code":"return msg;"}`},
		{"http_nodes", `{"id":"x","type":"http","method":"GET","url":"https://fixture.invalid/status","target":"result"}`},
		{"control_nodes", `{"id":"x","type":"control","target_id":"nonexistent-fixture-target","value":1}`},
	} {
		fd := `{"graph":{"nodes":[{"id":"t","type":"trigger","device_id":"fixture","point_id":"temp","op":">","value":0},` + feature.node + `,{"id":"n","type":"notify","channel_id":"absent-fixture-channel","message":"synthetic"}],"edges":[{"from":"t","to":"x"},{"from":"x","to":"n"}]}}`
		if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET definition=$2 WHERE id=$1`, id+"-v2", fd); err != nil {
			t.Fatal(err)
		}
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO tenant_features(tenant_id,feature,enabled) VALUES($1,$2,false) ON CONFLICT(tenant_id,feature) DO UPDATE SET enabled=false`, tenant, feature.name); err != nil {
			t.Fatal(err)
		}
		if w := call(limitedAPI, tenant, "admin", "POST", "/v1/flows/"+id+"/publish", `{"version":2}`); w.Code != 403 {
			t.Fatalf("%s disabled code=%d body=%s", feature.name, w.Code, w.Body.String())
		}
		check(id + "-v1")
		if _, err := s.st.Pool.Exec(ctx, `UPDATE tenant_features SET enabled=true WHERE tenant_id=$1 AND feature=$2`, tenant, feature.name); err != nil {
			t.Fatal(err)
		}
		if w := call(limitedAPI, tenant, "operator", "POST", "/v1/flows/"+id+"/publish", `{"version":2}`); w.Code != 403 {
			t.Fatalf("%s operator code=%d body=%s", feature.name, w.Code, w.Body.String())
		}
		check(id + "-v1")
		if w := call(limitedAPI, tenant, "admin", "POST", "/v1/flows/"+id+"/publish", `{"version":2}`); w.Code != 200 {
			t.Fatalf("%s admin code=%d body=%s", feature.name, w.Code, w.Body.String())
		}
		check(id + "-v2")
		if w := call(api, tenant, "operator", "POST", "/v1/flows/"+id+"/rollback", `{"version":1}`); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		check(id + "-v1")
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET definition=$2 WHERE id=$1`, id+"-v2", def); err != nil {
		t.Fatal(err)
	}
	// A pointer update failure rolls back the status changes too.
	if _, err := s.st.Pool.Exec(ctx, `CREATE FUNCTION itest_publish_refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='itest-flow-publish-guard-flow' AND NEW.published_version_id IS DISTINCT FROM OLD.published_version_id THEN RAISE EXCEPTION 'synthetic publish refusal'; END IF; RETURN NEW; END $$;CREATE TRIGGER itest_publish_refuse BEFORE UPDATE ON flows FOR EACH ROW EXECUTE FUNCTION itest_publish_refuse()`); err != nil {
		t.Fatal(err)
	}
	if w := call(api, tenant, "operator", "POST", "/v1/flows/"+id+"/publish", `{"version":2}`); w.Code != 500 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(id + "-v1")
	if _, err := s.st.Pool.Exec(ctx, `DROP TRIGGER itest_publish_refuse ON flows;DROP FUNCTION itest_publish_refuse()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		var wg sync.WaitGroup
		errs := make(chan string, 2)
		for _, v := range []int{2, 3} {
			wg.Add(1)
			go func(v int) {
				defer wg.Done()
				w := call(api, tenant, "operator", "POST", "/v1/flows/"+id+"/publish", fmt.Sprintf(`{"version":%d}`, v))
				if w.Code != 200 {
					errs <- fmt.Sprintf("%d %s", w.Code, w.Body.String())
				}
			}(v)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Fatal(e)
		}
		var pointer string
		if err := s.st.Pool.QueryRow(ctx, `SELECT published_version_id FROM flows WHERE id=$1`, id).Scan(&pointer); err != nil {
			t.Fatal(err)
		}
		if pointer != id+"-v2" && pointer != id+"-v3" {
			t.Fatal(pointer)
		}
		check(pointer)
	}
	if w := call(api, tenant, "operator", "POST", "/v1/flows/"+id+"/rollback", `{"version":1}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(id + "-v1")
	var runs int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id=$1`, id).Scan(&runs); err != nil || runs != 0 {
		t.Fatal(runs, err)
	}
}
