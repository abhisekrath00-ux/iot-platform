package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

// Synthetic invalid DB pointers exercise the real loaders, not a candidate
// snapshot. Notification channels do not exist and the notifier is a stub.
func TestIntegrationPublishedFlowLoaderFences(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-flow-loader-fence"
	const foreign = "itest-flow-loader-foreign"
	seed(t, s, tenant)
	seed(t, s, foreign)
	id, other := tenant+"-flow", tenant+"-other"
	cleanup := func(ctx context.Context) {
		for _, q := range []string{`UPDATE flows SET published_version_id=NULL WHERE id=ANY($1)`, `DELETE FROM flows WHERE id=ANY($1)`} {
			if _, err := s.st.Pool.Exec(ctx, q, []string{id, other}); err != nil {
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
	for _, f := range []string{id, other} {
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,'loader fixture','{}','test-user');`, f, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES($1,$2,$3,1,'{}','published','test-user')`, f+"-v", f, tenant); err != nil {
			t.Fatal(err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/flows/{id}/export", s.exportFlow)
	api.HandleFunc("GET /v1/flows/{id}/export-nodered", s.exportNodeRED)
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprintf("scheduled=%t", scheduled), func(t *testing.T) {
			def := `{"trigger":{"device_id":"` + tenant + `-dev","point_id":"temp","op":">","value":0},"steps":[{"type":"notify","channel_id":"missing-fixture-channel","message":"synthetic"}]}`
			if scheduled {
				def = `{"graph":{"nodes":[{"id":"i","type":"inject","seconds":3600,"value":7,"device_id":"fixture","point_id":"temp"},{"id":"n","type":"notify","channel_id":"missing-fixture-channel","message":"synthetic"}],"edges":[{"from":"i","to":"n"}]}}`
			}
			for _, tc := range []struct {
				name, status, versionTenant, pointer string
				enabled, accepted                    bool
			}{
				{"valid published", "published", tenant, id + "-v", true, true},
				{"draft pointer", "draft", tenant, id + "-v", true, false},
				{"archived pointer", "archived", tenant, id + "-v", true, false},
				{"foreign version tenant", "published", foreign, id + "-v", true, false},
				{"other flow pointer", "published", tenant, other + "-v", true, false},
				{"disabled flow", "published", tenant, id + "-v", false, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if _, err := s.st.Pool.Exec(ctx, `DELETE FROM flow_runs WHERE flow_id=$1`, id); err != nil {
						t.Fatal(err)
					}
					if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET definition=$2,status='published',tenant_id=$3 WHERE flow_id=ANY($1)`, []string{id, other}, def, tenant); err != nil {
						t.Fatal(err)
					}
					if _, err := s.st.Pool.Exec(ctx, `UPDATE flow_versions SET status=$2,tenant_id=$3 WHERE id=$1`, id+"-v", tc.status, tc.versionTenant); err != nil {
						t.Fatal(err)
					}
					if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=$2,enabled=$3 WHERE id=$1`, id, tc.pointer, tc.enabled); err != nil {
						t.Fatal(err)
					}
					if scheduled {
						flow.RunScheduled(ctx, s.st.Pool, nopNotifier{})
					} else {
						flow.EvaluateWith(ctx, s.st.Pool, nopNotifier{}, nil, tenant, tenant+"-dev", "temp", 42)
					}
					var runs int
					if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id=$1`, id).Scan(&runs); err != nil {
						t.Fatal(err)
					}
					want := 0
					if tc.accepted {
						want = 1
					}
					if runs != want {
						t.Errorf("runs=%d want=%d", runs, want)
					}
					// Export is allowed for disabled but still correctly published flows.
					wantCode := 404
					if tc.accepted || tc.name == "disabled flow" {
						wantCode = 200
					}
					for _, suffix := range []string{"export", "export-nodered"} {
						w := call(api, tenant, "viewer", "GET", "/v1/flows/"+id+"/"+suffix, "")
						if w.Code != wantCode {
							t.Errorf("%s code=%d want=%d: %s", suffix, w.Code, wantCode, w.Body.String())
						}
						if w := call(api, foreign, "viewer", "GET", "/v1/flows/"+id+"/"+suffix, ""); w.Code != 404 {
							t.Errorf("foreign caller %s code=%d", suffix, w.Code)
						}
					}
				})
			}
		})
	}
}
