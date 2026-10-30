package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type catalogRowsFixture struct {
	n, i                  int
	scanErr, iterationErr error
}

func (f *catalogRowsFixture) Next() bool { f.i++; return f.i <= f.n }
func (f *catalogRowsFixture) Scan(dest ...any) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	for i, d := range dest {
		*d.(*string) = fmt.Sprintf("row%d-column%d", f.i, i)
	}
	return nil
}
func (f *catalogRowsFixture) Err() error { return f.iterationErr }
func TestPointCatalogReadsAllOrNothing(t *testing.T) {
	for _, test := range []struct {
		name string
		rows catalogRowsFixture
		want int
		err  error
	}{
		{"empty", catalogRowsFixture{}, 0, nil},
		{"limit", catalogRowsFixture{n: 5000}, 5000, nil},
		{"overflow", catalogRowsFixture{n: 5001}, 0, errPointCatalogLimit},
		{"scan", catalogRowsFixture{n: 2, scanErr: errors.New("scan")}, 0, errors.New("scan")},
		{"iteration", catalogRowsFixture{n: 2, iterationErr: errors.New("iteration")}, 0, errors.New("iteration")},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := readPointCatalog(&test.rows)
			if test.err != nil {
				if err == nil || out != nil || err.Error() != test.err.Error() {
					t.Fatalf("partial result accepted %d %v", len(out), err)
				}
			} else if err != nil || out == nil || len(out) != test.want {
				t.Fatalf("rows=%d err=%v", len(out), err)
			}
		})
	}
}
func TestIntegrationPointCatalogOverflowNotCached(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-catalog")
	seed(t, s, "itest-catalog-other")
	s.cache = newCache()
	mux := http.NewServeMux()
	s.cached(mux, "GET /v1/points", s.listPoints)
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO points(id,device_id,unit) SELECT 'catalog-'||lpad(g::text,5,'0'),'itest-catalog-dev','C' FROM generate_series(1,5000) g ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	w := call(mux, "itest-catalog", "viewer", "GET", "/v1/points", "")
	if w.Code != 422 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Cache") != "" || strings.Contains(w.Body.String(), "catalog-00001") {
		t.Fatalf("overflow falsely complete: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.st.Pool.Exec(t.Context(), `DELETE FROM points WHERE device_id='itest-catalog-dev' AND id LIKE 'catalog-%'`); err != nil {
		t.Fatal(err)
	}
	// Without invalidation, the next read must recover: failed result was not cached.
	w = call(mux, "itest-catalog", "viewer", "GET", "/v1/points", "")
	if w.Code != 200 || w.Header().Get("X-Cache") != "MISS" || !strings.Contains(w.Body.String(), "itest-catalog-dev") || strings.Contains(w.Body.String(), "itest-catalog-other") {
		t.Fatalf("recovery/tenant boundary: %d %s", w.Code, w.Body.String())
	}
	if w = call(mux, "itest-catalog", "viewer", "GET", "/v1/points", ""); w.Header().Get("X-Cache") != "HIT" {
		t.Fatal("successful catalog cache regressed")
	}
	s.cache.Invalidate("itest-catalog")
	s.st.Close()
	w = call(mux, "itest-catalog", "viewer", "GET", "/v1/points", "")
	if w.Code != 500 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Cache") != "" || strings.Contains(w.Body.String(), "closed") {
		t.Fatalf("outage became success/leaked error: %d %s", w.Code, w.Body.String())
	}
}
