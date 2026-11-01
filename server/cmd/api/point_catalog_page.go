package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

type pointCatalogCursor struct {
	Tenant string `json:"t"`
	Device string `json:"d"`
	Point  string `json:"p"`
}
type pointCatalogPage struct {
	Items      []pointCatalogRow `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func parsePointCatalogPage(r *http.Request) (int, pointCatalogCursor, error) {
	var c pointCatalogCursor
	q := r.URL.Query()
	size, err := strconv.Atoi(q.Get("page_size"))
	if err != nil || size < 1 || size > 1000 || len(q["page_size"]) != 1 {
		return 0, c, fmt.Errorf("page_size must be one integer from 1 to 1000")
	}
	if len(q["after"]) > 1 {
		return 0, c, fmt.Errorf("after must occur at most once")
	}
	if raw := q.Get("after"); raw != "" {
		if len(raw) > 8192 {
			return 0, c, fmt.Errorf("invalid catalog cursor")
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || json.Unmarshal(b, &c) != nil || c.Tenant != auth.Tenant(r) || c.Device == "" || c.Point == "" {
			return 0, c, fmt.Errorf("invalid catalog cursor")
		}
	}
	return size, c, nil
}

// Opt-in bounded keyset pages preserve the legacy array contract. Each page is
// a current read, not a cross-request snapshot; callers must restart after edits.
// C collation makes the cursor and ORDER BY use the same stable byte ordering.
func (s *server) listPointsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	size, c, err := parsePointCatalogPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT d.id,d.name,p.id,COALESCE(p.unit,'')
 FROM devices d JOIN points p ON p.device_id=d.id
 WHERE d.tenant_id=$1 AND ($2='' OR (d.id COLLATE "C",p.id COLLATE "C") > ($2 COLLATE "C",$3 COLLATE "C"))
 ORDER BY d.id COLLATE "C",p.id COLLATE "C" LIMIT $4`, auth.Tenant(r), c.Device, c.Point, size+1)
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	page := pointCatalogPage{Items: make([]pointCatalogRow, 0, size)}
	for rows.Next() {
		var p pointCatalogRow
		if err := rows.Scan(&p.DeviceID, &p.DeviceName, &p.PointID, &p.Unit); err != nil {
			dbErr(w, err)
			return
		}
		if len(page.Items) == size {
			last := page.Items[len(page.Items)-1]
			b, _ := json.Marshal(pointCatalogCursor{auth.Tenant(r), last.DeviceID, last.PointID})
			page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		page.Items = append(page.Items, p)
	}
	if err := rows.Err(); err != nil {
		dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
