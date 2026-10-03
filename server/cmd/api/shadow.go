package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// A device "shadow" here is reported state only: the last value of each point the device
// sent, how old it is, plus server-side attributes. There is no desired state and nothing
// here is ever sent to a device. Commands stay on the approval + four-eyes path.

var attrKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,39}$`)

// validateAttributes accepts at most 32 keys with string (max 256 chars), number or
// boolean values. Nested objects and arrays are refused.
func validateAttributes(a map[string]any) error {
	if len(a) > 32 {
		return fmt.Errorf("at most 32 attributes")
	}
	for k, v := range a {
		if !attrKeyRe.MatchString(k) {
			return fmt.Errorf("attribute name %q: use 1-40 chars, start with a letter, then letters, digits . _ -", k)
		}
		switch x := v.(type) {
		case string:
			if len(x) > 256 {
				return fmt.Errorf("attribute %q is longer than 256 characters", k)
			}
		case float64, bool:
		default:
			return fmt.Errorf("attribute %q must be a string, number or boolean", k)
		}
	}
	return nil
}

// shadowStale: a reading is stale when it is older than three polling intervals; with no
// known interval, older than 15 minutes.
func shadowStale(age time.Duration, intervalSeconds float64) bool {
	limit := 15 * time.Minute
	if intervalSeconds > 0 {
		limit = time.Duration(3*intervalSeconds) * time.Second
		if limit < 30*time.Second {
			limit = 30 * time.Second
		}
	}
	return age > limit
}

// GET /v1/devices/{id}/shadow: reported state and attributes.
func (s *server) getShadow(w http.ResponseWriter, r *http.Request) {
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	var name string
	var interval *float64
	var attrRaw []byte
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT name, (config->'connection'->>'interval_seconds')::float8, attributes FROM devices WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&name, &interval, &attrRaw)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	iv := 0.0
	if interval != nil {
		iv = *interval
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT DISTINCT ON (point_id) point_id, value, unit, quality, observed_at
		 FROM telemetry WHERE tenant_id=$1 AND device_id=$2 AND observed_at > now() - interval '7 days'
		 ORDER BY point_id, observed_at DESC`, tenant, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	now := time.Now()
	reported := map[string]any{}
	var lastSeen *time.Time
	for rows.Next() {
		var pid, unit, quality string
		var v float64
		var at time.Time
		if rows.Scan(&pid, &v, &unit, &quality, &at) != nil {
			continue
		}
		age := now.Sub(at)
		reported[pid] = map[string]any{"value": v, "unit": unit, "quality": quality, "observed_at": at,
			"age_seconds": int(age.Seconds()), "stale": shadowStale(age, iv)}
		if lastSeen == nil || at.After(*lastSeen) {
			t := at
			lastSeen = &t
		}
	}
	attrs := map[string]any{}
	_ = json.Unmarshal(attrRaw, &attrs)
	writeJSON(w, 200, map[string]any{"device_id": id, "name": name, "reported": reported, "attributes": attrs, "last_seen": lastSeen})
}

// PUT /v1/devices/{id}/attributes (admin, operator): replace the device's attributes.
func (s *server) setDeviceAttributes(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil || in.Attributes == nil {
		http.Error(w, "bad json: expected {\"attributes\": {...}}", 400)
		return
	}
	if err := validateAttributes(in.Attributes); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, _ := json.Marshal(in.Attributes)
	id := r.PathValue("id")
	ct, err := s.st.Pool.Exec(r.Context(), `UPDATE devices SET attributes=$1 WHERE id=$2 AND tenant_id=$3`, raw, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "device.attributes", id, map[string]any{"count": len(in.Attributes)})
	writeJSON(w, 200, map[string]any{"attributes": in.Attributes})
}
