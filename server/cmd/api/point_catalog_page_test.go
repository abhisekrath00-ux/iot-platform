package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPointCatalogPageParameters(t *testing.T) {
	for _, q := range []string{"page_size=0", "page_size=1001", "page_size=oops", "page_size=1&page_size=2", "after=x", "page_size=2&after=bad", "page_size=2&after=x&after=y", "page_size=2&after=" + strings.Repeat("a", 8193)} {
		r := httptest.NewRequest("GET", "/v1/points?"+q, nil)
		if _, _, err := parsePointCatalogPage(r); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
	for _, size := range []string{"1", "1000"} {
		if _, _, err := parsePointCatalogPage(httptest.NewRequest("GET", "/v1/points?page_size="+size, nil)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegrationPointCatalogKeysetPages(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-page")
	seed(t, s, "itest-page-other")
	s.cache = newCache()
	mux := http.NewServeMux()
	s.cached(mux, "GET /v1/points", s.listPoints)
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO points(id,device_id,unit) SELECT 'page-'||lpad(g::text,5,'0'),'itest-page-dev','C' FROM generate_series(1,5001)g`); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	after := ""
	pages := 0
	for {
		path := "/v1/points?page_size=1000"
		if after != "" {
			path += "&after=" + url.QueryEscape(after)
		}
		w := call(mux, "itest-page", "viewer", "GET", path, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Cache") == "HIT" {
			t.Fatalf("page %d %s", w.Code, w.Body.String())
		}
		var page pointCatalogPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 1000 || page.Items == nil {
			t.Fatal("invalid page size")
		}
		for _, p := range page.Items {
			if p.DeviceID != "itest-page-dev" || seen[p.PointID] {
				t.Fatalf("duplicate/tenant leak %+v", p)
			}
			seen[p.PointID] = true
		}
		pages++
		if pages > 7 {
			t.Fatal("cursor cycle")
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == after {
			t.Fatal("cursor repeated")
		}
		after = page.NextCursor
	}
	if len(seen) != 5002 || pages != 6 {
		t.Fatalf("incomplete pages: %d %d", len(seen), pages)
	}
	// Cursors are not credentials. Replaying another tenant's cursor is refused;
	// a forged same-tenant position can only skip its own tenant's rows.
	b, _ := json.Marshal(pointCatalogCursor{"itest-page-other", "itest-page-dev", "page-00001"})
	forged := base64.RawURLEncoding.EncodeToString(b)
	if w := call(mux, "itest-page", "viewer", "GET", "/v1/points?page_size=1&after="+forged, ""); w.Code != 400 {
		t.Fatal("foreign cursor accepted")
	}
	if w := call(mux, "itest-page-other", "viewer", "GET", "/v1/points?page_size=1000", ""); w.Code != 200 || strings.Contains(w.Body.String(), "page-00001") {
		t.Fatal("tenant page isolation")
	}
	if w := call(mux, "itest-page", "viewer", "GET", "/v1/points", ""); w.Code != 422 {
		t.Fatal("legacy completeness contract changed")
	}
	s.st.Close()
	if w := call(mux, "itest-page", "viewer", "GET", "/v1/points?page_size=1", ""); w.Code != 500 || strings.Contains(w.Body.String(), "closed") {
		t.Fatal("outage leaked/accepted")
	}
}
