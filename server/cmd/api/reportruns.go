package main

import (
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// listReportRuns exposes execution state, not raw error text (which may contain
// transport targets or provider details). It does not prove recipient delivery.
func (s *server) listReportRuns(w http.ResponseWriter, r *http.Request) {
	if scopeOf(r) != "" {
		http.Error(w, "not available to customer-scoped users", 403)
		return
	}
	tenant, id := auth.Tenant(r), r.PathValue("id")
	var exists bool
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM reports WHERE tenant_id=$1 AND id=$2)`, tenant, id).Scan(&exists); err != nil {
		dbErr(w, err)
		return
	}
	if !exists {
		http.Error(w, "report not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT r.id,r.started_at,r.finished_at,r.status,r.row_count FROM report_runs r JOIN reports p ON p.id=r.report_id WHERE p.tenant_id=$1 AND p.id=$2 ORDER BY r.started_at DESC,r.id DESC LIMIT 50`, tenant, id)
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID         string     `json:"id"`
		StartedAt  time.Time  `json:"started_at"`
		FinishedAt *time.Time `json:"finished_at"`
		Status     string     `json:"status"`
		RowCount   *int       `json:"row_count"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.StartedAt, &x.FinishedAt, &x.Status, &x.RowCount); err != nil {
			dbErr(w, err)
			return
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		dbErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"runs": out, "limit": 50})
}
