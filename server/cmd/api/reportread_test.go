package main

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"testing"
	"time"
)

type reportFaultRows struct {
	pgx.Rows
	n                int
	scanErr, iterErr error
	empty            bool
}

func (r *reportFaultRows) Next() bool { r.n++; return !r.empty && r.n == 1 }
func (r *reportFaultRows) Err() error { return r.iterErr }
func (r *reportFaultRows) Scan(d ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if len(d) == 9 {
		*d[0].(*string) = "synthetic-report"
		*d[1].(*string) = "Synthetic"
		*d[2].(*[]byte) = []byte(`{"metrics":[]}`)
		cron, channel, customer := "0 8 * * *", "synthetic-channel", "synthetic-customer"
		*d[3].(**string) = &cron
		*d[4].(**string) = &channel
		*d[5].(**time.Time) = nil
		*d[6].(*time.Time) = time.Unix(0, 0)
		*d[7].(*int) = 2
		*d[8].(**string) = &customer
	} else {
		*d[0].(*int) = 1
		*d[1].(*string) = "Earlier"
		*d[2].(*[]byte) = []byte(`{"metrics":[]}`)
		*d[3].(**string) = nil
		*d[4].(**string) = nil
		*d[5].(*string) = "synthetic-user"
		*d[6].(*time.Time) = time.Unix(0, 0)
	}
	return nil
}
func TestReportReadersRejectPartialData(t *testing.T) {
	fault := errors.New("synthetic failure")
	for _, tc := range []struct {
		name       string
		scan, iter error
	}{{"scan", fault, nil}, {"iteration", nil, fault}} {
		t.Run(tc.name, func(t *testing.T) {
			if rows, err := readReportList(&reportFaultRows{scanErr: tc.scan, iterErr: tc.iter}, ""); err == nil || rows != nil {
				t.Fatalf("partial list %v %v", rows, err)
			}
			if rows, err := readReportHistory(&reportFaultRows{scanErr: tc.scan, iterErr: tc.iter}); err == nil || rows != nil {
				t.Fatalf("partial history %v %v", rows, err)
			}
		})
	}
	for _, scoped := range []string{"", "customer"} {
		rows, err := readReportList(&reportFaultRows{}, scoped)
		if err != nil || len(rows) != 1 {
			t.Fatalf("valid list %v %v", rows, err)
		}
		if scoped != "" {
			if rows[0]["channel_id"].(*string) != nil || rows[0]["schedule_cron"].(*string) != nil {
				t.Fatal("scoped delivery settings leaked")
			}
		}
	}
	rows, err := readReportHistory(&reportFaultRows{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("valid history %v %v", rows, err)
	}
	rows, err = readReportList(&reportFaultRows{empty: true}, "")
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty list must be []", rows, err)
	}
	rows, err = readReportHistory(&reportFaultRows{empty: true})
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty history must be []", rows, err)
	}
}
func TestIntegrationReportReadUnavailableFails(t *testing.T) {
	s, _ := testServer(t)
	s.st.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports", s.listReports)
	mux.HandleFunc("GET /v1/reports/{id}/versions", s.listReportVersions)
	for _, p := range []string{"/v1/reports", "/v1/reports/synthetic/versions"} {
		w := call(mux, "itest-reportread", "admin", "GET", p, "")
		if w.Code != 500 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "not found") || strings.Contains(w.Body.String(), "current_state") {
			t.Fatalf("false report read %d %v %s", w.Code, w.Header(), w.Body)
		}
	}
}

func TestIntegrationReportReadSnapshotShape(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	tenant := "itest-reportread"
	seed(t, s, tenant)
	def := []byte(`{"metrics":[{"device_id":"itest-reportread-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"}`)
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('reportread',$1,'Original',$2,'test-user')`, tenant, def); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.writeReportVersion(ctx, tenant, "reportread", "test-user", reportState{name: "Current", definition: def}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports", s.listReports)
	mux.HandleFunc("GET /v1/reports/{id}/versions", s.listReportVersions)
	w := call(mux, tenant, "admin", "GET", "/v1/reports/reportread/versions", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"current":2`) || !strings.Contains(w.Body.String(), `"name":"Original"`) || !strings.Contains(w.Body.String(), `"name":"Current"`) {
		t.Fatalf("history %d %v %s", w.Code, w.Header(), w.Body)
	}
	w = call(mux, tenant, "admin", "GET", "/v1/reports", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"version":2`) {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	for _, other := range []struct{ tenant, path string }{{tenant, "/v1/reports/missing/versions"}, {"itest-other-reportread", "/v1/reports/reportread/versions"}} {
		w = call(mux, other.tenant, "admin", "GET", other.path, "")
		if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing/foreign %d %v %s", w.Code, w.Header(), w.Body)
		}
	}
}
