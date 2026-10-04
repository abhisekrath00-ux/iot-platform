package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Quotas: per-tenant resource limits set by the operator (tenantctl quota). The database trigger in
// migration 0058 is the atomic enforcement; the checks here give a clear 409 before an insert and feed the
// usage page. There is no payment or billing anywhere in this path.

type quotaKind struct {
	Resource string
	column   string
	count    string // SQL counting current use, $1 = tenant
}

var quotaKinds = []quotaKind{
	{"devices", "max_devices", `SELECT count(*) FROM devices WHERE tenant_id=$1`},
	{"users", "max_users", `SELECT (SELECT count(*) FROM users WHERE tenant_id=$1) + (SELECT count(*) FROM user_invites WHERE tenant_id=$1 AND used_at IS NULL AND expires_at > now())`},
	{"api_keys", "max_api_keys", `SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND revoked_at IS NULL AND expires_at > now()`},
	{"customers", "max_customers", `SELECT count(*) FROM customers WHERE tenant_id=$1`},
}

// quotaLimit returns the tenant's limit for a kind (its own row, else the '*' default); nil = unlimited.
func (s *server) quotaLimit(ctx context.Context, tenant string, k quotaKind) *int {
	var lim *int
	s.st.Pool.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE((SELECT %[1]s FROM tenant_quotas WHERE tenant_id=$1), (SELECT %[1]s FROM tenant_quotas WHERE tenant_id='*'))`, k.column), tenant).Scan(&lim)
	return lim
}

// quotaOK reports whether adding n more of a resource stays within the limit. When it does not it writes
// the 409 itself and returns false.
func (s *server) quotaOK(w http.ResponseWriter, r *http.Request, resource string, n int) bool {
	for _, k := range quotaKinds {
		if k.Resource != resource {
			continue
		}
		t := auth.Tenant(r)
		lim := s.quotaLimit(r.Context(), t, k)
		if lim == nil {
			return true
		}
		var cur int
		s.st.Pool.QueryRow(r.Context(), k.count, t).Scan(&cur)
		if cur+n > *lim {
			http.Error(w, fmt.Sprintf("quota exceeded: this workspace is limited to %d %s (in use: %d). Ask your platform operator to raise it.", *lim, resource, cur), http.StatusConflict)
			return false
		}
		return true
	}
	return true
}

// GET /v1/usage (admin): current use against each limit, plus 24h and 30d ingest volume.
func (s *server) usage(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	t := auth.Tenant(r)
	type row struct {
		Resource string `json:"resource"`
		Used     int    `json:"used"`
		Limit    *int   `json:"limit"`
	}
	rows := []row{}
	for _, k := range quotaKinds {
		var used int
		s.st.Pool.QueryRow(r.Context(), k.count, t).Scan(&used)
		rows = append(rows, row{k.Resource, used, s.quotaLimit(r.Context(), t, k)})
	}
	var p24, p30 int64
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id=$1 AND observed_at > now() - interval '24 hours'`, t).Scan(&p24)
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id=$1 AND observed_at > now() - interval '30 days'`, t).Scan(&p30)
	writeJSON(w, 200, map[string]any{"quotas": rows, "points_24h": p24, "points_30d": p30,
		"note": "Limits are set by the platform operator. Nothing here is billed."})
}
