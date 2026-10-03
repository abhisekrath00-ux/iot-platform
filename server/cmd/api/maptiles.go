package main

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Map tiles come from a tile server the deployer runs inside their own network (for example
// a self-hosted OpenStreetMap or TileServer GL instance), set with MAP_TILE_URL such as
// http://tiles.internal/{z}/{x}/{y}.png. The browser never talks to it: the API fetches and relays
// tiles, so the page's content security policy stays "self only" and the tile host need not be
// reachable from user devices. The template is deployment configuration, never user input.
// Nothing is bundled: without MAP_TILE_URL the Map page keeps its plain canvas.

const (
	maxTileBytes = 512 << 10
	maxTileZoom  = 19
)

var tileClient = &http.Client{
	Timeout: 5 * time.Second,
	// A tile server that redirects is refused rather than followed.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// tileURL fills the template. It returns "" when no valid template is configured or the tile
// coordinates are out of range for the zoom.
func tileURL(tmpl string, z, x, y int) string {
	if tmpl == "" || z < 0 || z > maxTileZoom || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return ""
	}
	for _, p := range []string{"{z}", "{x}", "{y}"} {
		if !strings.Contains(tmpl, p) {
			return ""
		}
	}
	u := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(tmpl)
	if p, err := url.Parse(u); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return ""
	}
	return u
}

// GET /v1/map/config: whether tiles are available, and the attribution text the deployer set.
func (s *server) mapConfig(w http.ResponseWriter, r *http.Request) {
	on := tileURL(os.Getenv("MAP_TILE_URL"), 0, 0, 0) != ""
	writeJSON(w, 200, map[string]any{"tiles": on, "attribution": os.Getenv("MAP_TILE_ATTRIBUTION")})
}

// GET /v1/map/tiles/{z}/{x}/{y}: relays one tile. Any signed-in user may read it.
func (s *server) mapTile(w http.ResponseWriter, r *http.Request) {
	z, e1 := strconv.Atoi(r.PathValue("z"))
	x, e2 := strconv.Atoi(r.PathValue("x"))
	y, e3 := strconv.Atoi(r.PathValue("y"))
	u := ""
	if e1 == nil && e2 == nil && e3 == nil {
		u = tileURL(os.Getenv("MAP_TILE_URL"), z, x, y)
	}
	if u == "" {
		http.Error(w, "no such tile, or no tile server configured", 404)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", u, nil)
	resp, err := tileClient.Do(req)
	if err != nil {
		http.Error(w, "tile server unreachable", 502)
		return
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode != 200 || (ct != "image/png" && ct != "image/jpeg" && ct != "image/webp") {
		http.Error(w, "tile server did not return an image", 502)
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxTileBytes+1))
	if err != nil || len(b) > maxTileBytes {
		http.Error(w, "tile too large", 502)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(b)
}
