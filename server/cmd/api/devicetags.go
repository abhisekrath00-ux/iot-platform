package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,31}$`)

// normalizeTags lower-cases, validates, de-duplicates and sorts tags.
// At most 20 tags per device.
func normalizeTags(in []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if !tagRe.MatchString(t) {
			return nil, false
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) > 20 {
		return nil, false
	}
	sort.Strings(out)
	return out, true
}

// PUT /v1/devices/{id}/tags replaces the device's tags.
func (s *server) setDeviceTags(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	tags, ok := normalizeTags(in.Tags)
	if !ok {
		http.Error(w, "tags must be 1-32 chars of a-z 0-9 . _ : - (max 20)", 400)
		return
	}
	id := r.PathValue("id")
	ct, err := s.st.Pool.Exec(r.Context(), `UPDATE devices SET tags=$1 WHERE id=$2 AND tenant_id=$3`, tags, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "device.tags", id, map[string]any{"tags": tags})
	writeJSON(w, 200, map[string]any{"id": id, "tags": tags})
}
