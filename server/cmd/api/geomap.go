package main

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

const maxGeofences = 100

func validLatLon(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && !math.IsNaN(lat) && !math.IsNaN(lon)
}

// haversine returns the great-circle distance in metres.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// PUT /v1/devices/{id}/location  {"lat":..,"lon":..} or {"lat":null,"lon":null} to clear.
func (s *server) setDeviceLocation(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Lat *float64 `json:"lat"`
		Lon *float64 `json:"lon"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || (in.Lat == nil) != (in.Lon == nil) {
		http.Error(w, "send lat and lon together, or both null to clear", 400)
		return
	}
	if in.Lat != nil && !validLatLon(*in.Lat, *in.Lon) {
		http.Error(w, "lat must be -90..90 and lon -180..180", 400)
		return
	}
	ct, err := s.st.Pool.Exec(r.Context(), `UPDATE devices SET lat=$1, lon=$2 WHERE id=$3 AND tenant_id=$4`, in.Lat, in.Lon, r.PathValue("id"), auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "device not found", 404)
		return
	}
	if in.Lat != nil {
		if err := store.RecordTrail(r.Context(), s.st.Pool, auth.Tenant(r), r.PathValue("id"), *in.Lat, *in.Lon, time.Now(), "manual"); err != nil {
			log.Printf("position trail: %v", err)
		}
	}
	s.audit(r, "device.location", r.PathValue("id"), map[string]any{"lat": in.Lat, "lon": in.Lon})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// PUT /v1/devices/{id}/location-source  {"lat_point":"lat","lon_point":"lon"}
// or both null to clear. Names the two telemetry points that carry the
// device's reported position; ingest then keeps lat/lon current (up to about
// a minute for the config to reach ingest). The manual position stays as-is
// until the first matching reading arrives.
func (s *server) setDeviceLocationSource(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		LatPoint *string `json:"lat_point"`
		LonPoint *string `json:"lon_point"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || (in.LatPoint == nil) != (in.LonPoint == nil) {
		http.Error(w, "send lat_point and lon_point together, or both null to clear", 400)
		return
	}
	if in.LatPoint != nil {
		if *in.LatPoint == "" || *in.LonPoint == "" || len(*in.LatPoint) > 128 || len(*in.LonPoint) > 128 || *in.LatPoint == *in.LonPoint {
			http.Error(w, "two different point ids, each 1-128 chars", 400)
			return
		}
		var ok bool
		if err := s.st.Pool.QueryRow(r.Context(),
			`SELECT EXISTS(
			   SELECT 1 FROM devices d
			   JOIN points plat ON plat.device_id=d.id AND plat.id=$2
			   JOIN points plon ON plon.device_id=d.id AND plon.id=$3
			   WHERE d.id=$1 AND d.tenant_id=$4)`,
			r.PathValue("id"), *in.LatPoint, *in.LonPoint, auth.Tenant(r)).Scan(&ok); err != nil || !ok {
			http.Error(w, "device not found, or a named point does not belong to it", 404)
			return
		}
	}
	ct, err := s.st.Pool.Exec(r.Context(), `UPDATE devices SET lat_point=$1, lon_point=$2 WHERE id=$3 AND tenant_id=$4`,
		in.LatPoint, in.LonPoint, r.PathValue("id"), auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "device not found", 404)
		return
	}
	if s.pos != nil {
		// This process picks the change up at once; the MQTT worker's cache
		// expires within store.PositionTTL.
		s.pos.Reset()
	}
	s.audit(r, "device.location_source", r.PathValue("id"), map[string]any{"lat_point": in.LatPoint, "lon_point": in.LonPoint})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// fenceIn is the create/update body for a zone. A zone is a circle (lat/lon/
// radius_m) or, when polygon is given, a polygon: lat/lon/radius_m are then
// derived from the vertices and used only for map framing - membership is the
// polygon. Update semantics: a body without polygon writes a circle and clears
// any polygon the zone had; the UI always sends full form state.
type fenceIn struct {
	Name     string      `json:"name"`
	Lat      float64     `json:"lat"`
	Lon      float64     `json:"lon"`
	Radius   float64     `json:"radius_m"`
	Polygon  [][]float64 `json:"polygon"`
	OnEnter  bool        `json:"alert_on_enter"`
	OnExit   bool        `json:"alert_on_exit"`
	Severity string      `json:"alert_severity"`
}

// validateFenceIn normalises and checks a zone body. For a polygon zone it
// replaces lat/lon/radius with the polygon's frame.
func validateFenceIn(in *fenceIn) (poly []rules.Vertex, errMsg string) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || strings.ContainsAny(in.Name, "<>\x00") {
		return nil, "name (up to 80) required"
	}
	if in.Polygon != nil {
		for _, v := range in.Polygon {
			if len(v) != 2 {
				return nil, "polygon vertices are [lat, lon] pairs"
			}
			poly = append(poly, rules.Vertex{v[0], v[1]})
		}
		if err := rules.ValidatePolygon(poly); err != nil {
			return nil, err.Error()
		}
		in.Lat, in.Lon, in.Radius = rules.PolygonFrame(poly)
		return poly, ""
	}
	if !validLatLon(in.Lat, in.Lon) || !(in.Radius >= 1 && in.Radius <= 100000) {
		return nil, "valid lat/lon and radius 1-100000 m required"
	}
	return nil, ""
}

func validSeverity(s string) bool { return s == "info" || s == "warning" || s == "critical" }

func (s *server) createGeofence(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in fenceIn
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	poly, errMsg := validateFenceIn(&in)
	if errMsg != "" {
		http.Error(w, errMsg, 400)
		return
	}
	if in.Severity == "" {
		in.Severity = "warning"
	}
	if !validSeverity(in.Severity) {
		http.Error(w, "alert_severity must be info, warning or critical", 400)
		return
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM geofences WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&n)
	if n >= maxGeofences {
		http.Error(w, "too many geofences", 409)
		return
	}
	id := uuid.NewString()
	var polyJSON any
	if poly != nil {
		polyJSON = poly
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO geofences(id,tenant_id,name,lat,lon,radius_m,created_by,alert_on_enter,alert_on_exit,alert_severity,polygon) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, auth.Tenant(r), in.Name, in.Lat, in.Lon, in.Radius, auth.User(r), in.OnEnter, in.OnExit, in.Severity, polyJSON); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "geofence.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

// PUT /v1/geofences/{id}: update a zone's geometry or its enter/exit alerts.
func (s *server) updateGeofence(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in fenceIn
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	poly, errMsg := validateFenceIn(&in)
	if errMsg != "" {
		http.Error(w, errMsg, 400)
		return
	}
	if !validSeverity(in.Severity) {
		http.Error(w, "alert_severity must be info, warning or critical", 400)
		return
	}
	var polyJSON any
	if poly != nil {
		polyJSON = poly
	}
	ct, err := s.st.Pool.Exec(r.Context(),
		`UPDATE geofences SET name=$1, lat=$2, lon=$3, radius_m=$4, alert_on_enter=$5, alert_on_exit=$6, alert_severity=$7, polygon=$10 WHERE id=$8 AND tenant_id=$9`,
		in.Name, in.Lat, in.Lon, in.Radius, in.OnEnter, in.OnExit, in.Severity, r.PathValue("id"), auth.Tenant(r), polyJSON)
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "geofence.update", r.PathValue("id"), map[string]any{"name": in.Name})
	w.WriteHeader(204)
}

func (s *server) deleteGeofence(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	ct, err := s.st.Pool.Exec(r.Context(), `DELETE FROM geofences WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "geofence.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// GET /v1/map: located devices, geofences, and which zones each device is inside (computed now).
func (s *server) getMap(w http.ResponseWriter, r *http.Request) {
	tenant := auth.Tenant(r)
	type fence struct {
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Lat      float64        `json:"lat"`
		Lon      float64        `json:"lon"`
		Radius   float64        `json:"radius_m"`
		Polygon  []rules.Vertex `json:"polygon,omitempty"`
		OnEnter  bool           `json:"alert_on_enter"`
		OnExit   bool           `json:"alert_on_exit"`
		Severity string         `json:"alert_severity"`
		poly     []rules.Vertex // membership shape: polygon when set, else the circle above
	}
	fences := []fence{}
	fr, err := s.st.Pool.Query(r.Context(), `SELECT id,name,lat,lon,radius_m,polygon,alert_on_enter,alert_on_exit,alert_severity FROM geofences WHERE tenant_id=$1 ORDER BY name`, tenant)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	for fr.Next() {
		var f fence
		var polyRaw []byte
		if fr.Scan(&f.ID, &f.Name, &f.Lat, &f.Lon, &f.Radius, &polyRaw, &f.OnEnter, &f.OnExit, &f.Severity) != nil {
			continue
		}
		if len(polyRaw) > 0 && json.Unmarshal(polyRaw, &f.poly) == nil && len(f.poly) > 0 {
			f.Polygon = f.poly
		}
		fences = append(fences, f)
	}
	fr.Close()
	dr, err := s.st.Pool.Query(r.Context(), `SELECT id, COALESCE(NULLIF(name,''),id), lat, lon FROM devices WHERE tenant_id=$1 AND lat IS NOT NULL AND lon IS NOT NULL ORDER BY id LIMIT 2000`, tenant)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer dr.Close()
	devs := []map[string]any{}
	for dr.Next() {
		var id, name string
		var lat, lon float64
		if dr.Scan(&id, &name, &lat, &lon) != nil {
			continue
		}
		inside := []string{}
		for _, f := range fences {
			in := len(f.poly) > 0 && rules.PointInPolygon(lat, lon, f.poly)
			if len(f.poly) == 0 {
				in = haversine(lat, lon, f.Lat, f.Lon) <= f.Radius
			}
			if in {
				inside = append(inside, f.Name)
			}
		}
		devs = append(devs, map[string]any{"id": id, "name": name, "lat": lat, "lon": lon, "inside": inside})
	}
	writeJSON(w, 200, map[string]any{"devices": devs, "geofences": fences,
		"note": "Positions are set by people, or reported live when a device names its latitude and longitude points. Zones are circles or polygons; a zone can alert when a device enters or leaves it."})
}

// getDeviceTrack returns at most 720 real recorded samples, including both
// ends of the window. SQL window ranking bounds application memory on long
// trails. Lines joining samples are visual connections, not known routes.
func (s *server) getDeviceTrack(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		n, err := strconv.Atoi(h)
		if err != nil || n < 1 || n > 168 {
			http.Error(w, "hours must be 1-168", 400)
			return
		}
		hours = n
	}
	tenant, dev := auth.Tenant(r), r.PathValue("id")
	var exists bool
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM devices WHERE tenant_id=$1 AND id=$2)`, tenant, dev).Scan(&exists); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if !exists {
		http.Error(w, "device not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `WITH ranked AS (
 SELECT lat,lon,recorded_at,source,row_number() OVER (ORDER BY recorded_at) AS rn,count(*) OVER () AS n
 FROM device_positions WHERE tenant_id=$1 AND device_id=$2 AND recorded_at >= now()-make_interval(hours => $3) AND recorded_at<=now())
 SELECT lat,lon,recorded_at,source,n FROM ranked
 WHERE n<=720 OR rn=1 OR rn=n OR mod(rn-1,ceil((n-1)::numeric/719)::bigint)=0 ORDER BY recorded_at`, tenant, dev, hours)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	type point struct {
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
		At     string  `json:"at"`
		Source string  `json:"source"`
	}
	points := []point{}
	var total int
	for rows.Next() {
		var p point
		var at time.Time
		if err := rows.Scan(&p.Lat, &p.Lon, &at, &p.Source, &total); err != nil {
			http.Error(w, "db", 500)
			return
		}
		p.At = at.UTC().Format(time.RFC3339Nano)
		points = append(points, p)
	}
	if rows.Err() != nil {
		http.Error(w, "db", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"device_id": dev, "hours": hours, "points": points, "total": total})
}
