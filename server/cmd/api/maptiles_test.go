package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTileURL(t *testing.T) {
	const tm = "http://tiles.internal/{z}/{x}/{y}.png"
	if got := tileURL(tm, 3, 2, 5); got != "http://tiles.internal/3/2/5.png" {
		t.Fatalf("got %q", got)
	}
	for name, u := range map[string]string{
		"x out of range": tileURL(tm, 3, 8, 0),
		"negative":       tileURL(tm, 3, -1, 0),
		"zoom too deep":  tileURL(tm, 20, 0, 0),
		"empty":          tileURL("", 1, 0, 0),
		"missing {y}":    tileURL("http://t/{z}/{x}.png", 1, 0, 0),
		"file scheme":    tileURL("file:///etc/{z}/{x}/{y}", 1, 0, 0),
		"no host":        tileURL("http:///{z}/{x}/{y}", 1, 0, 0),
	} {
		if u != "" {
			t.Errorf("%s accepted: %q", name, u)
		}
	}
}

func TestMapTileRelay(t *testing.T) {
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/1/0/1.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG fake"))
		case "/1/1/1.png":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<script>x</script>"))
		case "/1/0/0.png":
			http.Redirect(w, r, "http://169.254.169.254/", 302)
		}
	}))
	defer up.Close()
	t.Setenv("MAP_TILE_URL", up.URL+"/{z}/{x}/{y}.png")
	t.Setenv("MAP_TILE_ATTRIBUTION", "test tiles")
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/map/config", s.mapConfig)
	mux.HandleFunc("GET /v1/map/tiles/{z}/{x}/{y}", s.mapTile)
	if w := call(mux, "t", "viewer", "GET", "/v1/map/config", ""); !strings.Contains(w.Body.String(), `"tiles":true`) || !strings.Contains(w.Body.String(), "test tiles") {
		t.Fatalf("config %s", w.Body)
	}
	w := call(mux, "t", "viewer", "GET", "/v1/map/tiles/1/0/1", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("tile %d %v", w.Code, w.Header())
	}
	if c := call(mux, "t", "viewer", "GET", "/v1/map/tiles/1/1/1", "").Code; c != 502 {
		t.Fatalf("non-image upstream = %d, want 502", c)
	}
	if c := call(mux, "t", "viewer", "GET", "/v1/map/tiles/1/0/0", "").Code; c != 502 {
		t.Fatalf("redirecting upstream = %d, want 502", c)
	}
	before := hits
	if c := call(mux, "t", "viewer", "GET", "/v1/map/tiles/1/9/9", "").Code; c != 404 || hits != before {
		t.Fatal("out-of-range tile reached the upstream or was not 404")
	}
	if c := call(mux, "t", "viewer", "GET", "/v1/map/tiles/a/b/c", "").Code; c != 404 {
		t.Fatalf("garbage coords = %d", c)
	}
	t.Setenv("MAP_TILE_URL", "")
	if w := call(mux, "t", "viewer", "GET", "/v1/map/config", ""); !strings.Contains(w.Body.String(), `"tiles":false`) {
		t.Fatalf("unconfigured: %s", w.Body)
	}
	if c := call(mux, "t", "viewer", "GET", "/v1/map/tiles/1/0/1", "").Code; c != 404 {
		t.Fatalf("unconfigured tile = %d", c)
	}
}
