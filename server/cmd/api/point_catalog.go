package main

import (
	"errors"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

const pointCatalogLimit = 5000

var errPointCatalogLimit = errors.New("point catalog limit exceeded")

type pointCatalogRow struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	PointID    string `json:"point_id"`
	Unit       string `json:"unit"`
}

type pointCatalogRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

// Buffer the complete bounded result before writing a response. A failed Scan,
// iteration or overflow must never become a successful partial cached catalog.
func readPointCatalog(rows pointCatalogRows) ([]pointCatalogRow, error) {
	out := make([]pointCatalogRow, 0)
	for rows.Next() {
		var p pointCatalogRow
		if err := rows.Scan(&p.DeviceID, &p.DeviceName, &p.PointID, &p.Unit); err != nil {
			return nil, err
		}
		if len(out) == pointCatalogLimit {
			return nil, errPointCatalogLimit
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// listPoints feeds unscoped report/dashboard pickers. customerScope's allowlist
// still refuses this route for customer-scoped users; do not expand that grant.
func (s *server) listPoints(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("page_size") || r.URL.Query().Has("after") {
		s.listPointsPage(w, r)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT d.id,d.name,p.id,COALESCE(p.unit,'') FROM devices d JOIN points p ON p.device_id=d.id
 WHERE d.tenant_id=$1 ORDER BY d.name,d.id,p.id LIMIT 5001`, auth.Tenant(r))
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		dbErr(w, err)
		return
	}
	defer rows.Close()
	out, err := readPointCatalog(rows)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		if errors.Is(err, errPointCatalogLimit) {
			http.Error(w, "point catalog exceeds 5000 rows; complete catalog unavailable", http.StatusUnprocessableEntity)
			return
		}
		dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
