package main

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Embedding supplies unused pgx.Rows methods; these readers only need Next/Scan/Err.
type obsFaultRows struct {
	pgx.Rows
	n                int
	scanErr, iterErr error
}

func (r *obsFaultRows) Next() bool { r.n++; return r.n <= 1 }
func (r *obsFaultRows) Err() error { return r.iterErr }
func (r *obsFaultRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if len(dest) == 5 {
		*dest[0].(*int64) = 1
		*dest[1].(*time.Time) = time.Unix(0, 0)
		*dest[2].(*string) = "api"
		*dest[3].(*string) = "error"
		*dest[4].(*string) = "synthetic"
	} else {
		*dest[0].(*time.Time) = time.Unix(0, 0)
		*dest[1].(*string) = "error"
		*dest[2].(*string) = "synthetic token=abcdef123456"
	}
	return nil
}
func TestObsReadersRejectPartialData(t *testing.T) {
	fault := errors.New("synthetic failure")
	for _, tc := range []struct {
		name       string
		scan, iter error
	}{{"scan", fault, nil}, {"iteration", nil, fault}} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := readObsLogs(&obsFaultRows{scanErr: tc.scan, iterErr: tc.iter})
			if err == nil || rows != nil {
				t.Fatalf("partial log result: %v %v", rows, err)
			}
			text, err := readObsSupportLogs(&obsFaultRows{scanErr: tc.scan, iterErr: tc.iter})
			if err == nil || text != "" {
				t.Fatalf("partial bundle: %q %v", text, err)
			}
		})
	}
	rows, err := readObsLogs(&obsFaultRows{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("valid logs: %v %v", rows, err)
	}
	text, err := readObsSupportLogs(&obsFaultRows{})
	if err != nil || !strings.Contains(text, "synthetic") || strings.Contains(text, "abcdef123456") {
		t.Fatalf("valid redacted bundle: %q %v", text, err)
	}
}
func TestIntegrationObsUnavailableDoesNotDownload(t *testing.T) {
	s, _ := testServer(t)
	s.st.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /logs", s.systemLogs)
	mux.HandleFunc("GET /support", s.systemSupport)
	for _, path := range []string{"/logs", "/support"} {
		w := call(mux, "itest-obs-fail", "admin", "GET", path, "")
		if w.Code != 500 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Disposition") != "" || strings.Contains(w.Body.String(), "HexThings support bundle") || strings.Contains(w.Body.String(), `"logs"`) {
			t.Fatalf("%s false success: %d %v %s", path, w.Code, w.Header(), w.Body.String())
		}
	}
}
