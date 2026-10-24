package main

import (
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGroupCSVSafeAndEmpty(t *testing.T) {
	v := -1.25
	w := httptest.NewRecorder()
	writeGroupSeriesCSV(w, "=group,\"x\"", " \t@point", "sum", []map[string]any{{"t": time.Date(2026, 10, 24, 1, 0, 0, 0, time.FixedZone("offset", 3600)), "v": &v, "unit": "+unit", "devices": 1, "members": 2, "samples": 3}})
	r, e := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if e != nil || len(r) != 2 {
		t.Fatal(e)
	}
	if r[1][0] != "'=group,\"x\"" || r[1][1] != "' \t@point" || r[1][4] != "2026-10-24T00:00:00Z" || r[1][5] != "-1.25" || r[1][6] != "'+unit" {
		t.Fatal(r)
	}
	w = httptest.NewRecorder()
	writeGroupSeriesCSV(w, "x", "y", "sum", nil)
	r, e = csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if e != nil || len(r) != 1 {
		t.Fatal("empty csv", e)
	}
}
