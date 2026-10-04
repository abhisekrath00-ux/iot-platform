package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Customer hierarchy and scoped users. See docs/customers-design.md for the design and security review.
//
// A scoped user (a row in user_customer_scope) is confined by default deny: customerScope below lets only a
// short allowlist of read endpoints through, and each of those either checks the device against the user's
// customer subtree or filters its query by it. Everything else answers 403, including every write.

const maxCustomerDepth = 6

type scopeKey struct{}

// scopeOf returns the customer id a request is confined to, or "" for an unscoped (tenant-wide) request.
func scopeOf(r *http.Request) string { v, _ := r.Context().Value(scopeKey{}).(string); return v }

// customerSubtreeSQL is a CTE named csub: the scoped customer and everything below it. $1 must be the tenant.
const customerSubtreeCTE = `csub AS (
  SELECT id FROM customers WHERE tenant_id=$1 AND id=%s
  UNION ALL SELECT c.id FROM customers c JOIN csub ON c.parent_id=csub.id WHERE c.tenant_id=$1)`

func (s *server) deviceInScope(ctx context.Context, tenant, customer, device string) bool {
	var ok bool
	err := s.st.Pool.QueryRow(ctx, `WITH RECURSIVE `+strings.Replace(customerSubtreeCTE, "%s", "$2", 1)+`
		SELECT EXISTS (SELECT 1 FROM devices WHERE tenant_id=$1 AND id=$3 AND customer_id IN (SELECT id FROM csub))`,
		tenant, customer, device).Scan(&ok)
	return err == nil && ok
}

var (
	scopedDevicePath     = regexp.MustCompile(`^/v1/devices/([^/]+)/health$`)
	scopedAlertPath      = regexp.MustCompile(`^/v1/alerts/([^/]+)$`)
	scopedReportDownload = regexp.MustCompile(`^/v1/reports/[^/]+/download$`)
	scopedTelemetry      = map[string]bool{"latest": true, "series": true, "count": true, "rollup": true, "anomalies": true, "forecast": true, "related": true}
)

// scopedAllows is the allowlist: the only requests a customer-scoped user may make. Anything else is refused.
func scopedAllows(method, p string) bool {
	if method == http.MethodPost && (p == "/v1/me/password" || p == "/v1/me/sessions/revoke") { // a scoped user can still change their own password
		return true
	}
	if method != http.MethodGet {
		return false
	}
	switch p {
	case "/v1/devices", "/v1/alerts", "/v1/features", "/v1/map/config", "/v1/me", "/v1/dashboards", "/v1/reports":
		return true
	}
	if scopedReportDownload.MatchString(p) {
		return true
	}
	return (strings.HasPrefix(p, "/v1/telemetry/") && scopedTelemetry[strings.TrimPrefix(p, "/v1/telemetry/")]) ||
		scopedDevicePath.MatchString(p) || scopedAlertPath.MatchString(p)
}

func (s *server) customerScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, user := auth.Tenant(r), auth.User(r)
		var cid string
		err := s.st.Pool.QueryRow(r.Context(), `SELECT customer_id FROM user_customer_scope WHERE tenant_id=$1 AND user_id=$2`, tenant, user).Scan(&cid)
		if errors.Is(err, pgx.ErrNoRows) {
			next.ServeHTTP(w, r)
			return
		}
		if err != nil { // fail closed: an unknown scope is not "no scope"
			http.Error(w, "scope lookup failed", http.StatusServiceUnavailable)
			return
		}
		p := r.URL.Path
		if !scopedAllows(r.Method, p) {
			http.Error(w, "not available to customer-scoped users", http.StatusForbidden)
			return
		}
		dev := ""
		switch {
		case strings.HasPrefix(p, "/v1/telemetry/"):
			dev = r.URL.Query().Get("device_id")
			if dev == "" {
				http.Error(w, "device_id required", 400)
				return
			}
		case scopedDevicePath.MatchString(p):
			dev = scopedDevicePath.FindStringSubmatch(p)[1]
		case scopedAlertPath.MatchString(p):
			var ok bool
			s.st.Pool.QueryRow(r.Context(), `WITH RECURSIVE `+strings.Replace(customerSubtreeCTE, "%s", "$2", 1)+`
				SELECT EXISTS (SELECT 1 FROM alerts a JOIN devices d ON d.id=a.device_id AND d.tenant_id=a.tenant_id
				 WHERE a.tenant_id=$1 AND a.id=$3 AND d.customer_id IN (SELECT id FROM csub))`,
				tenant, cid, scopedAlertPath.FindStringSubmatch(p)[1]).Scan(&ok)
			if !ok {
				http.Error(w, "not found", 404)
				return
			}
		}
		if dev != "" && !s.deviceInScope(r.Context(), tenant, cid, dev) {
			http.Error(w, "not found", 404)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), scopeKey{}, cid)))
	})
}

func validCustomerName(n string) bool {
	return n != "" && len(n) <= 128 && !strings.ContainsAny(n, "<>\x00")
}

func (s *server) listCustomers(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT c.id, c.parent_id, c.name,
		(SELECT count(*) FROM devices d WHERE d.customer_id=c.id),
		COALESCE((SELECT array_agg(user_id ORDER BY user_id) FROM user_customer_scope u WHERE u.customer_id=c.id), '{}')
		FROM customers c WHERE c.tenant_id=$1 ORDER BY c.name LIMIT 5000`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var parent *string
		var n int
		var users []string
		rows.Scan(&id, &parent, &name, &n, &users)
		out = append(out, map[string]any{"id": id, "parent_id": parent, "name": name, "devices": n, "users": users})
	}
	writeJSON(w, 200, out)
}

func (s *server) createCustomer(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", 403)
		}
		return
	}
	if !s.quotaOK(w, r, "customers", 1) {
		return
	}
	var in struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parent_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !validCustomerName(in.Name) {
		http.Error(w, "name required (up to 128 characters, no markup)", 400)
		return
	}
	t := auth.Tenant(r)
	if in.ParentID != nil && *in.ParentID != "" {
		var depth int
		err := s.st.Pool.QueryRow(r.Context(), `WITH RECURSIVE up AS (
			SELECT id, parent_id, 1 AS d FROM customers WHERE tenant_id=$1 AND id=$2
			UNION ALL SELECT c.id, c.parent_id, up.d+1 FROM customers c JOIN up ON c.id=up.parent_id WHERE c.tenant_id=$1)
			SELECT COALESCE(max(d),0) FROM up`, t, *in.ParentID).Scan(&depth)
		if err != nil || depth == 0 {
			http.Error(w, "parent customer not found", 404)
			return
		}
		if depth >= maxCustomerDepth {
			http.Error(w, "customer tree too deep", 400)
			return
		}
	} else {
		in.ParentID = nil
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO customers(id,tenant_id,parent_id,name) VALUES($1,$2,$3,$4)`, id, t, in.ParentID, in.Name); err != nil {
		http.Error(w, "a customer with that name already exists there", 409)
		return
	}
	s.audit(r, "customer.create", id, map[string]any{"id": id, "name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", 403)
		}
		return
	}
	t, id := auth.Tenant(r), r.PathValue("id")
	var kids, devs int
	s.st.Pool.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM customers WHERE tenant_id=$1 AND parent_id=$2), (SELECT count(*) FROM devices WHERE tenant_id=$1 AND customer_id=$2)`, t, id).Scan(&kids, &devs)
	if kids > 0 || devs > 0 {
		http.Error(w, "customer still has sub-customers or devices", 409)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM customers WHERE tenant_id=$1 AND id=$2`, t, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "customer not found", 404)
		return
	}
	s.audit(r, "customer.delete", id, map[string]any{"id": id})
	w.WriteHeader(204)
}

// setDeviceCustomer: PUT /v1/devices/{id}/customer {"customer_id": "..."|null}
func (s *server) setDeviceCustomer(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", 403)
		}
		return
	}
	var in struct {
		CustomerID *string `json:"customer_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if in.CustomerID != nil && *in.CustomerID == "" {
		in.CustomerID = nil
	}
	t := auth.Tenant(r)
	if in.CustomerID != nil {
		var ok bool
		s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM customers WHERE tenant_id=$1 AND id=$2)`, t, *in.CustomerID).Scan(&ok)
		if !ok {
			http.Error(w, "customer not found", 404)
			return
		}
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE devices SET customer_id=$3 WHERE tenant_id=$1 AND id=$2`, t, r.PathValue("id"), in.CustomerID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "device not found", 404)
		return
	}
	s.audit(r, "device.customer", r.PathValue("id"), map[string]any{"device": r.PathValue("id"), "customer": in.CustomerID})
	w.WriteHeader(204)
}

// setCustomerUser: PUT /v1/customers/{id}/users/{user} scopes that user to the customer subtree;
// DELETE /v1/customers/{id}/users/{user} makes them tenant-wide again. Admin session only.
func (s *server) setCustomerUser(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", 403)
		}
		return
	}
	t, id, user := auth.Tenant(r), r.PathValue("id"), r.PathValue("user")
	if user == "" || len(user) > 128 {
		http.Error(w, "bad user", 400)
		return
	}
	if r.Method == http.MethodDelete {
		s.st.Pool.Exec(r.Context(), `DELETE FROM user_customer_scope WHERE tenant_id=$1 AND user_id=$2 AND customer_id=$3`, t, user, id)
		s.audit(r, "customer.user.remove", id, map[string]any{"customer": id, "user": user})
		w.WriteHeader(204)
		return
	}
	var ok bool
	s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM customers WHERE tenant_id=$1 AND id=$2)`, t, id).Scan(&ok)
	if !ok {
		http.Error(w, "customer not found", 404)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES($1,$2,$3)
		ON CONFLICT (tenant_id,user_id) DO UPDATE SET customer_id=EXCLUDED.customer_id`, t, user, id); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "customer.user.set", id, map[string]any{"customer": id, "user": user})
	w.WriteHeader(204)
}

// layoutLeaks returns the first device id found in the layout that lies outside the customer a dashboard is
// shared with ("" when the dashboard is not shared or the layout is clean). The match is a substring test on
// the layout text, so it errs on the side of refusing.
func (s *server) layoutLeaks(ctx context.Context, tenant, dashboard, layout string) string {
	var bad string
	s.st.Pool.QueryRow(ctx, `WITH RECURSIVE `+strings.Replace(customerSubtreeCTE, "%s", "(SELECT customer_id FROM dashboards WHERE id=$2 AND tenant_id=$1)", 1)+`
		SELECT d.id FROM devices d WHERE d.tenant_id=$1 AND (SELECT customer_id FROM dashboards WHERE id=$2 AND tenant_id=$1) IS NOT NULL
		  AND (d.customer_id IS NULL OR d.customer_id NOT IN (SELECT id FROM csub)) AND strpos($3, d.id) > 0 LIMIT 1`, tenant, dashboard, layout).Scan(&bad)
	return bad
}

// PUT /v1/dashboards/{id}/customer {customer_id|null}: share a dashboard with one customer subtree. Admin session only.
func (s *server) setDashboardCustomer(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		if auth.ViaKey(r) {
			http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		}
		return
	}
	var in struct {
		CustomerID *string `json:"customer_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	t, id := auth.Tenant(r), r.PathValue("id")
	var layout string
	if s.st.Pool.QueryRow(r.Context(), `SELECT layout::text FROM dashboards WHERE id=$1 AND tenant_id=$2`, id, t).Scan(&layout) != nil {
		http.Error(w, "not found", 404)
		return
	}
	if in.CustomerID != nil {
		var ok bool
		s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1 AND tenant_id=$2)`, *in.CustomerID, t).Scan(&ok)
		if !ok {
			http.Error(w, "unknown customer", 404)
			return
		}
		// refuse a layout that names a device outside the customer
		var bad string
		s.st.Pool.QueryRow(r.Context(), `WITH RECURSIVE `+strings.Replace(customerSubtreeCTE, "%s", "$2", 1)+`
			SELECT d.id FROM devices d WHERE d.tenant_id=$1 AND (d.customer_id IS NULL OR d.customer_id NOT IN (SELECT id FROM csub)) AND strpos($3, d.id) > 0 LIMIT 1`,
			t, *in.CustomerID, layout).Scan(&bad)
		if bad != "" {
			http.Error(w, "the dashboard references a device outside that customer: "+bad, 409)
			return
		}
	}
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE dashboards SET customer_id=$3 WHERE id=$1 AND tenant_id=$2`, id, t, in.CustomerID); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "dashboard.share", id, map[string]any{"customer_id": in.CustomerID})
	w.WriteHeader(204)
}

// PUT /v1/reports/{id}/customer {customer_id|null}: share a report with one customer subtree. Admin session
// only. Every metric must be a device of that subtree; a scoped user also gets no run-time overrides.
func (s *server) setReportCustomer(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		CustomerID *string `json:"customer_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	t, id := auth.Tenant(r), r.PathValue("id")
	var defBytes []byte
	if s.st.Pool.QueryRow(r.Context(), `SELECT definition FROM reports WHERE id=$1 AND tenant_id=$2`, id, t).Scan(&defBytes) != nil {
		http.Error(w, "not found", 404)
		return
	}
	if in.CustomerID != nil {
		var ok bool
		s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1 AND tenant_id=$2)`, *in.CustomerID, t).Scan(&ok)
		if !ok {
			http.Error(w, "unknown customer", 404)
			return
		}
		var def struct {
			Metrics []struct {
				DeviceID string `json:"device_id"`
			} `json:"metrics"`
		}
		json.Unmarshal(defBytes, &def)
		for _, m := range def.Metrics {
			if !s.deviceInScope(r.Context(), t, *in.CustomerID, m.DeviceID) {
				http.Error(w, "the report uses a device outside that customer: "+m.DeviceID, 409)
				return
			}
		}
	}
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE reports SET customer_id=$3 WHERE id=$1 AND tenant_id=$2`, id, t, in.CustomerID); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "report.share", id, map[string]any{"customer_id": in.CustomerID})
	w.WriteHeader(204)
}
