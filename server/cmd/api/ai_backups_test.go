package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

func TestIntegrationBackupProvidersFailOver(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-bk")
	ctx := t.Context()
	pool := s.st.Pool
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	s.secrets = &secrets.Store{Pool: pool, Key: key}
	clean := func() {
		for _, q := range []string{`DELETE FROM ai_backups WHERE tenant_id='itest-bk'`, `DELETE FROM ai_profiles WHERE tenant_id='itest-bk'`, `DELETE FROM secrets WHERE tenant_id='itest-bk'`, `DELETE FROM ai_settings WHERE tenant_id='itest-bk'`} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	llm.ResetCooldowns()
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/ai/profiles", s.listAIProfiles)
	api.HandleFunc("POST /v1/ai/profiles", s.saveAIProfile)
	api.HandleFunc("DELETE /v1/ai/profiles/{id}", s.deleteAIProfile)
	api.HandleFunc("PUT /v1/ai/backups", s.putAIBackups)
	s.inner = api

	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "quota", 429) }))
	defer limited.Close()
	good := &fakeModel{script: []map[string]any{{"role": "assistant", "content": "from backup"}}}
	goodSrv := httptest.NewServer(good)
	defer goodSrv.Close()

	if w := call(api, "itest-bk", "operator", "PUT", "/v1/ai/backups", `{"ids":[]}`); w.Code != 403 {
		t.Fatalf("operator: %d", w.Code)
	}
	w := call(api, "itest-bk", "admin", "POST", "/v1/ai/profiles", `{"name":"Backup B","base_url":"`+goodSrv.URL+`","model":"m-b","api_key":"sk-backup-BBB"}`)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var pid string
	pool.QueryRow(ctx, `SELECT id FROM ai_profiles WHERE tenant_id='itest-bk' AND name='Backup B'`).Scan(&pid)
	if w := call(api, "itest-bk", "admin", "PUT", "/v1/ai/backups", `{"ids":["nope"]}`); w.Code != 404 {
		t.Fatalf("unknown id: %d", w.Code)
	}
	if w := call(api, "itest-bk", "admin", "PUT", "/v1/ai/backups", `{"ids":["local","local"]}`); w.Code != 400 {
		t.Fatalf("duplicate: %d", w.Code)
	}
	if w := call(api, "itest-bk", "admin", "PUT", "/v1/ai/backups", `{"ids":["local","`+pid+`","local","x"]}`); w.Code != 400 {
		t.Fatalf("too many: %d", w.Code)
	}
	w = call(api, "itest-bk", "admin", "PUT", "/v1/ai/backups", `{"ids":["`+pid+`","local"]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"backups":["`+pid+`","local"]`) || strings.Contains(w.Body.String(), "sk-backup-BBB") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}

	// primary is rate limited: the run is answered by the backup, with the backup's own key
	cfg := llm.Config{BaseURL: limited.URL, Model: "m-a", APIKey: "sk-primary", Timeout: 0}
	res := s.runAgent(ctx, cfg, "itest-bk", "u", "viewer", []llm.Message{{Role: "user", Content: "how many devices are online?"}}, runCtx{via: "web"})
	if res.err != nil || res.reply != "from backup" || good.auth != "Bearer sk-backup-BBB" {
		t.Fatalf("failover: err=%v reply=%q auth=%q", res.err, res.reply, good.auth)
	}
	// the key is not in anything we return
	if strings.Contains(res.reply, "sk-") {
		t.Fatal("key leaked")
	}

	// no backups: the primary's error comes back as before
	call(api, "itest-bk", "admin", "PUT", "/v1/ai/backups", `{"ids":[]}`)
	llm.ResetCooldowns()
	res = s.runAgent(ctx, cfg, "itest-bk", "u", "viewer", []llm.Message{{Role: "user", Content: "how many devices are online?"}}, runCtx{via: "web"})
	if res.err == nil && !strings.Contains(res.reply, "429") && res.reply == "from backup" {
		t.Fatalf("expected no failover, got %q", res.reply)
	}

	// deleting a profile removes it from the list
	call(api, "itest-bk", "admin", "PUT", "/v1/ai/backups", `{"ids":["`+pid+`"]}`)
	if w := call(api, "itest-bk", "admin", "DELETE", "/v1/ai/profiles/"+pid, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"backups":[]`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}
