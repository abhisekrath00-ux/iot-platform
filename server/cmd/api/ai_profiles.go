package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// Saved AI provider profiles (admin, interactive session only). The key is write-only: it is stored
// encrypted per profile and never returned. One profile is active: it is copied into ai_settings.
// The built-in "local" profile is virtual (the bundled model) and needs no key.

const maxAIProfiles = 20

type aiProfile struct {
	ID, Name, BaseURL, Model, KeySecret, Capability string
}

func localProfile() aiProfile {
	base := os.Getenv("AI_LOCAL_BASE_URL")
	if base == "" {
		base = "http://ai-runtime:8090/v1"
	}
	model := os.Getenv("AI_LOCAL_MODEL")
	if model == "" {
		model = "qwen3-1.7b"
	}
	return aiProfile{ID: "local", Name: "Local model (built in)", BaseURL: base, Model: model, Capability: "small"}
}

func (s *server) aiProfileGet(ctx context.Context, tenant, id string) (aiProfile, bool) {
	if id == "local" {
		return localProfile(), true
	}
	p := aiProfile{ID: id}
	err := s.st.Pool.QueryRow(ctx, `SELECT name, base_url, model, key_secret, capability FROM ai_profiles WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&p.Name, &p.BaseURL, &p.Model, &p.KeySecret, &p.Capability)
	return p, err == nil
}

func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	return !auth.ViaKey(r) && requireRole(w, r, "admin")
}

// GET /v1/ai/profiles
func (s *server) listAIProfiles(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	tenant := auth.Tenant(r)
	cur, _ := s.aiConfigFor(r.Context(), tenant)
	out := []map[string]any{}
	add := func(p aiProfile, builtin bool) {
		active := cur.Enabled && cur.BaseURL == p.BaseURL && cur.Model == p.Model && cur.KeyName == p.KeySecret && capOrAuto(cur.Capability) == capOrAuto(p.Capability)
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "base_url": p.BaseURL, "model": p.Model, "has_key": p.KeySecret != "", "builtin": builtin, "active": active, "capability": capOrAuto(p.Capability), "small_model_mode": isSmallModel(aiConfig{BaseURL: p.BaseURL, Capability: p.Capability})})
	}
	add(localProfile(), true)
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id, name, base_url, model, key_secret, capability FROM ai_profiles WHERE tenant_id=$1 ORDER BY lower(name)`, tenant)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p aiProfile
			if rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.Model, &p.KeySecret, &p.Capability) == nil {
				add(p, false)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"profiles": out, "enabled": cur.Enabled, "backups": s.aiBackupIDs(r.Context(), tenant), "secrets_available": s.secrets != nil && len(s.secrets.Key) > 0})
}

type aiProfileIn struct {
	Name       string  `json:"name"`
	BaseURL    string  `json:"base_url"`
	Model      string  `json:"model"`
	APIKey     *string `json:"api_key"`
	ClearKey   bool    `json:"clear_key"`
	Capability string  `json:"capability"`
}

// POST /v1/ai/profiles (create) and PUT /v1/ai/profiles/{id} (edit; omit api_key to keep it).
func (s *server) saveAIProfile(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "local" {
		http.Error(w, "the built-in local profile cannot be edited", 400)
		return
	}
	var in aiProfileIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name, in.BaseURL, in.Model = strings.TrimSpace(in.Name), strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.Model)
	if in.Name == "" || len(in.Name) > 80 || in.Model == "" || len(in.Model) > 200 {
		http.Error(w, "a name and a model are required", 400)
		return
	}
	if err := llm.ValidateBaseURL(in.BaseURL); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Capability != "" && !validCapability(in.Capability) {
		http.Error(w, "capability must be auto, small or full", 400)
		return
	}
	tenant := auth.Tenant(r)
	var cur aiProfile
	if id == "" {
		var n int
		s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM ai_profiles WHERE tenant_id=$1`, tenant).Scan(&n)
		if n >= maxAIProfiles {
			http.Error(w, "too many profiles", 400)
			return
		}
		b := make([]byte, 6)
		rand.Read(b)
		id = "p" + hex.EncodeToString(b)
	} else {
		var ok bool
		if cur, ok = s.aiProfileGet(r.Context(), tenant, id); !ok {
			http.Error(w, "no such profile", 404)
			return
		}
	}
	capab := in.Capability
	if capab == "" {
		capab = capOrAuto(cur.Capability)
	}
	keyName, secretName := cur.KeySecret, "ai-key-"+id
	if in.ClearKey {
		keyName = ""
		if s.secrets != nil && len(s.secrets.Key) > 0 {
			s.st.Pool.Exec(r.Context(), `DELETE FROM secrets WHERE tenant_id=$1 AND name=$2`, tenant, secretName)
		}
	}
	if in.APIKey != nil && *in.APIKey != "" {
		st := s.secretStore(w)
		if st == nil {
			return
		}
		if err := st.Put(r.Context(), tenant, secretName, auth.User(r), []byte(*in.APIKey)); err != nil {
			http.Error(w, "could not store the key", 500)
			return
		}
		keyName = secretName
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO ai_profiles(tenant_id,id,name,base_url,model,key_secret,updated_by,capability) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id,id) DO UPDATE SET name=EXCLUDED.name, base_url=EXCLUDED.base_url, model=EXCLUDED.model, key_secret=EXCLUDED.key_secret, updated_by=EXCLUDED.updated_by, capability=EXCLUDED.capability, updated_at=now()`,
		tenant, id, in.Name, in.BaseURL, in.Model, keyName, auth.User(r), capab); err != nil {
		http.Error(w, "that name is already used", 409)
		return
	}
	s.audit(r, "ai.profile.save", id, map[string]any{"name": in.Name, "base_url": in.BaseURL, "model": in.Model, "key_changed": in.APIKey != nil || in.ClearKey})
	s.listAIProfiles(w, r)
}

// DELETE /v1/ai/profiles/{id}
func (s *server) deleteAIProfile(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	p, ok := s.aiProfileGet(r.Context(), tenant, id)
	if id == "local" || !ok {
		http.Error(w, "no such profile", 404)
		return
	}
	cur, _ := s.aiConfigFor(r.Context(), tenant)
	if cur.Enabled && cur.BaseURL == p.BaseURL && cur.Model == p.Model && cur.KeyName == p.KeySecret {
		http.Error(w, "this profile is active; switch to another one first", 409)
		return
	}
	s.st.Pool.Exec(r.Context(), `DELETE FROM ai_profiles WHERE tenant_id=$1 AND id=$2`, tenant, id)
	s.st.Pool.Exec(r.Context(), `DELETE FROM ai_backups WHERE tenant_id=$1 AND profile_id=$2`, tenant, id)
	if p.KeySecret != "" && s.secrets != nil && len(s.secrets.Key) > 0 {
		s.st.Pool.Exec(r.Context(), `DELETE FROM secrets WHERE tenant_id=$1 AND name=$2`, tenant, p.KeySecret)
	}
	s.audit(r, "ai.profile.delete", id, map[string]any{"name": p.Name})
	s.listAIProfiles(w, r)
}

// POST /v1/ai/profiles/{id}/activate: makes the profile the live assistant connection.
func (s *server) activateAIProfile(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	p, ok := s.aiProfileGet(r.Context(), tenant, id)
	if !ok {
		http.Error(w, "no such profile", 404)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO ai_settings(tenant_id,enabled,base_url,model,key_secret,updated_by,capability) VALUES($1,true,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id) DO UPDATE SET enabled=true, base_url=EXCLUDED.base_url, model=EXCLUDED.model, key_secret=EXCLUDED.key_secret, updated_by=EXCLUDED.updated_by, capability=EXCLUDED.capability, updated_at=now()`,
		tenant, p.BaseURL, p.Model, p.KeySecret, auth.User(r), capOrAuto(p.Capability)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "ai.profile.activate", id, map[string]any{"name": p.Name, "base_url": p.BaseURL, "model": p.Model})
	s.listAIProfiles(w, r)
}

// POST /v1/ai/profiles/{id}/test: one tiny request using that profile (does not switch to it).
func (s *server) testAIProfile(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	tenant := auth.Tenant(r)
	p, ok := s.aiProfileGet(r.Context(), tenant, r.PathValue("id"))
	if !ok {
		http.Error(w, "no such profile", 404)
		return
	}
	cfg, err := s.llmConfig(r.Context(), tenant, aiConfig{Enabled: true, BaseURL: p.BaseURL, Model: p.Model, KeyName: p.KeySecret, Capability: p.Capability})
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	m, err := llm.Chat(ctx, cfg, []llm.Message{{Role: "user", Content: "Reply with the single word: ok"}}, nil)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "reply": truncStr(m.Content, 200)})
}
