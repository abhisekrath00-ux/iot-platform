package main

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
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
	s.audit(r, "device.location", r.PathValue("id"), map[string]any{"lat": in.Lat, "lon": in.Lon})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *server) createGeofence(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name   string  `json:"name"`
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
		Radius float64 `json:"radius_m"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || strings.ContainsAny(in.Name, "<>\x00") || !validLatLon(in.Lat, in.Lon) || !(in.Radius >= 1 && in.Radius <= 100000) {
		http.Error(w, "name (up to 80), valid lat/lon and radius 1-100000 m required", 400)
		return
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM geofences WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&n)
	if n >= maxGeofences {
		http.Error(w, "too many geofences", 409)
		return
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO geofences(id,tenant_id,name,lat,lon,radius_m,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, auth.Tenant(r), in.Name, in.Lat, in.Lon, in.Radius, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "geofence.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
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
		ID     string  `json:"id"`
		Name   string  `json:"name"`
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
		Radius float64 `json:"radius_m"`
	}
	fences := []fence{}
	fr, err := s.st.Pool.Query(r.Context(), `SELECT id,name,lat,lon,radius_m FROM geofences WHERE tenant_id=$1 ORDER BY name`, tenant)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	for fr.Next() {
		var f fence
		if fr.Scan(&f.ID, &f.Name, &f.Lat, &f.Lon, &f.Radius) == nil {
			fences = append(fences, f)
		}
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
			if haversine(lat, lon, f.Lat, f.Lon) <= f.Radius {
				inside = append(inside, f.Name)
			}
		}
		devs = append(devs, map[string]any{"id": id, "name": name, "lat": lat, "lon": lon, "inside": inside})
	}
	writeJSON(w, 200, map[string]any{"devices": devs, "geofences": fences,
		"note": "Positions are set by people, not read from telemetry. Zones are circles; membership is computed when you load the map, with no enter/exit alerts."})
}
