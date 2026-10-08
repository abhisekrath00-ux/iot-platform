package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// Backup AI providers: an ordered list of saved profiles tried when the active one fails in a way
// another provider could fix (see llm.Failoverable). Admin only, interactive session only. A hosted
// backup sends the conversation to that provider, so it is an explicit admin choice per profile.

const maxAIBackups = 3

func (s *server) aiBackupIDs(ctx context.Context, tenant string) []string {
	ids := []string{}
	rows, err := s.st.Pool.Query(ctx, `SELECT profile_id FROM ai_backups WHERE tenant_id=$1 ORDER BY position`, tenant)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// PUT /v1/ai/backups {"ids":["p1","local"]}: replaces the ordered list.
func (s *server) putAIBackups(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if len(in.IDs) > maxAIBackups {
		http.Error(w, "at most 3 backup providers", 400)
		return
	}
	tenant := auth.Tenant(r)
	seen := map[string]bool{}
	for _, id := range in.IDs {
		if seen[id] {
			http.Error(w, "a provider can be listed once", 400)
			return
		}
		seen[id] = true
		if _, ok := s.aiProfileGet(r.Context(), tenant, id); !ok {
			http.Error(w, "no such profile: "+id, 404)
			return
		}
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `DELETE FROM ai_backups WHERE tenant_id=$1`, tenant); err != nil {
		http.Error(w, "database error", 500)
		return
	}
	for i, id := range in.IDs {
		if _, err := tx.Exec(r.Context(), `INSERT INTO ai_backups(tenant_id,position,profile_id) VALUES($1,$2,$3)`, tenant, i+1, id); err != nil {
			http.Error(w, "database error", 500)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "database error", 500)
		return
	}
	s.audit(r, "ai.backups.save", "", map[string]any{"ids": in.IDs})
	s.listAIProfiles(w, r)
}

// backupProviders builds the failover chain for a run: the active provider first, then the saved
// backups in order. A backup whose key cannot be read is skipped. The legacy env fallback is last.
func (s *server) backupProviders(ctx context.Context, tenant string, cfg llm.Config) llm.Provider {
	named := []llm.Named{{ID: llm.CooldownID(cfg.BaseURL, cfg.Model, cfg.APIKey), Provider: llm.OpenAICompat{Cfg: cfg}}}
	have := map[string]bool{named[0].ID: true}
	add := func(c llm.Config) {
		id := llm.CooldownID(c.BaseURL, c.Model, c.APIKey)
		if !have[id] {
			have[id] = true
			named = append(named, llm.Named{ID: id, Provider: llm.OpenAICompat{Cfg: c}})
		}
	}
	if s.st != nil && s.st.Pool != nil {
		for _, id := range s.aiBackupIDs(ctx, tenant) {
			p, ok := s.aiProfileGet(ctx, tenant, id)
			if !ok {
				continue
			}
			c, err := s.llmConfig(ctx, tenant, aiConfig{Enabled: true, BaseURL: p.BaseURL, Model: p.Model, KeyName: p.KeySecret, Capability: p.Capability})
			if err == nil {
				add(c)
			}
		}
	}
	if fb, ok := llm.FallbackFromEnv(cfg); ok {
		add(fb)
	}
	if len(named) == 1 {
		return named[0].Provider
	}
	return llm.Chain{Providers: named}
}
