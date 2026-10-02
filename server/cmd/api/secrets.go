package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

// Secrets are write-only: list returns names, put replaces, delete removes.
// Every change is audited with the name, never the value.
func (s *server) secretStore(w http.ResponseWriter) *secrets.Store {
	if s.secrets == nil || len(s.secrets.Key) == 0 {
		http.Error(w, "secrets are disabled: set SECRETS_KEY (base64, 32 bytes)", 503)
		return nil
	}
	return s.secrets
}

func (s *server) listSecrets(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	st := s.secretStore(w)
	if st == nil {
		return
	}
	out, err := st.List(r.Context(), auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) putSecret(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	st := s.secretStore(w)
	if st == nil {
		return
	}
	var in struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := r.PathValue("name")
	if err := st.Put(r.Context(), auth.Tenant(r), name, auth.User(r), []byte(in.Value)); err != nil {
		if errors.Is(err, secrets.ErrBadName) || errors.Is(err, secrets.ErrBadValue) {
			http.Error(w, err.Error(), 400)
			return
		}
		http.Error(w, "could not store secret", 500)
		return
	}
	s.audit(r, "secret.put", name, map[string]any{})
	writeJSON(w, 200, map[string]any{"name": name})
}

func (s *server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	st := s.secretStore(w)
	if st == nil {
		return
	}
	name := r.PathValue("name")
	ok, err := st.Delete(r.Context(), auth.Tenant(r), name)
	if err != nil {
		http.Error(w, "could not delete secret", 500)
		return
	}
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "secret.delete", name, map[string]any{})
	writeJSON(w, 200, map[string]any{"deleted": name})
}
