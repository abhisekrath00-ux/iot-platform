package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	got, ok := normalizeTags([]string{" Floor:2 ", "boiler", "boiler"})
	if !ok || strings.Join(got, ",") != "boiler,floor:2" {
		t.Fatalf("got %v %v", got, ok)
	}
	for _, bad := range [][]string{{""}, {"has space"}, {"-lead"}, {strings.Repeat("a", 33)}} {
		if _, ok := normalizeTags(bad); ok {
			t.Fatalf("%v accepted", bad)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "t" + string(rune('a'+i))
	}
	if _, ok := normalizeTags(many); ok {
		t.Fatal("21 tags accepted")
	}
}

func TestIntegrationDeviceTagsAndSearch(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tg1")
	seed(t, s, "itest-tg2")
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices", s.listDevices)
	api.HandleFunc("PUT /v1/devices/{id}/tags", s.setDeviceTags)

	if w := call(api, "itest-tg1", "viewer", "PUT", "/v1/devices/itest-tg1-dev/tags", `{"tags":["a"]}`); w.Code != 403 {
		t.Fatalf("viewer = %d", w.Code)
	}
	if w := call(api, "itest-tg2", "admin", "PUT", "/v1/devices/itest-tg1-dev/tags", `{"tags":["a"]}`); w.Code != 404 {
		t.Fatalf("cross-tenant = %d", w.Code)
	}
	if w := call(api, "itest-tg1", "admin", "PUT", "/v1/devices/itest-tg1-dev/tags", `{"tags":["bad tag"]}`); w.Code != 400 {
		t.Fatalf("invalid = %d", w.Code)
	}
	if w := call(api, "itest-tg1", "operator", "PUT", "/v1/devices/itest-tg1-dev/tags", `{"tags":["Plant-A","critical"]}`); w.Code != 200 {
		t.Fatalf("set = %d %s", w.Code, w.Body.String())
	}
	w := call(api, "itest-tg1", "viewer", "GET", "/v1/devices?tag=plant-a", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "itest-tg1-dev") || !strings.Contains(w.Body.String(), `"critical"`) {
		t.Fatalf("tag filter: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-tg1", "viewer", "GET", "/v1/devices?tag=nope", ""); strings.Contains(w.Body.String(), "itest-tg1-dev") {
		t.Fatalf("unmatched tag returned device: %s", w.Body.String())
	}
	if w := call(api, "itest-tg1", "viewer", "GET", "/v1/devices?q=boiler", ""); !strings.Contains(w.Body.String(), "itest-tg1-dev") {
		t.Fatalf("q search: %s", w.Body.String())
	}
	if w := call(api, "itest-tg2", "viewer", "GET", "/v1/devices?tag=plant-a", ""); strings.Contains(w.Body.String(), "itest-tg1-dev") {
		t.Fatalf("tenant leak: %s", w.Body.String())
	}
}

func TestIntegrationDeviceHealth(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-hl1")
	seed(t, s, "itest-hl2")
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices/{id}/health", s.deviceHealth)
	w := call(api, "itest-hl1", "viewer", "GET", "/v1/devices/itest-hl1-dev/health", "")
	// seeded telemetry is 1-5 minutes old at a 5s interval: stale, but has a score and three factors
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"freshness"`) || !strings.Contains(w.Body.String(), `"status"`) {
		t.Fatalf("health = %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-hl2", "viewer", "GET", "/v1/devices/itest-hl1-dev/health", ""); w.Code != 404 {
		t.Fatalf("cross-tenant = %d", w.Code)
	}
}
