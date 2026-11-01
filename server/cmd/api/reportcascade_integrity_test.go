package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationReportOptionsCompleteness(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-opt")
	seed(t, s, "itest-opt-other")
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO devices(id,tenant_id,gateway_id,profile,name) SELECT 'opt-'||lpad(g::text,5,'0'),'itest-opt','itest-opt-gw','modbus-tcp','Device' FROM generate_series(1,5000)g`); err != nil {
		t.Fatal(err)
	}
	w := call(h, "itest-opt", "viewer", "GET", "/v1/reports/options", "")
	if w.Code != 422 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "opt-00001") {
		t.Fatalf("partial options %d %s", w.Code, w.Body.String())
	}
	// Download cascading uses the same completeness guard, not a partial device set.
	w = call(h, "itest-opt", "admin", "POST", "/v1/reports", `{"name":"Options","definition":{"metrics":[{"device_id":"itest-opt-dev","point_id":"temp"}],"window_hours":2,"group_by":"hour"}}`)
	var rep struct {
		ID string `json:"id"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &rep) != nil {
		t.Fatalf("report %d %s", w.Code, w.Body.String())
	}
	if w = call(h, "itest-opt", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?site=itest-opt-site&format=csv", ""); w.Code != 422 {
		t.Fatalf("partial cascade %d %s", w.Code, w.Body.String())
	}
	if _, err := s.st.Pool.Exec(t.Context(), `DELETE FROM devices WHERE id='opt-05000'`); err != nil {
		t.Fatal(err)
	}
	w = call(h, "itest-opt", "viewer", "GET", "/v1/reports/options", "")
	var opt struct {
		Devices []cascadeDevice `json:"devices"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &opt) != nil || len(opt.Devices) != 5000 {
		t.Fatalf("exact bound %d %d", w.Code, len(opt.Devices))
	}
	w = call(h, "itest-opt-other", "viewer", "GET", "/v1/reports/options", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "opt-00001") {
		t.Fatal("tenant leak")
	}
	s.st.Close()
	w = call(h, "itest-opt", "viewer", "GET", "/v1/reports/options", "")
	if w.Code != http.StatusInternalServerError || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "closed") {
		t.Fatalf("outage %d %s", w.Code, w.Body.String())
	}
}

type cascadeRowsFixture struct {
	n, i             int
	scanErr, iterErr error
}

func (f *cascadeRowsFixture) Next() bool { f.i++; return f.i <= f.n }
func (f *cascadeRowsFixture) Scan(v ...any) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	*(v[0].(*string)) = "device"
	*(v[1].(*string)) = "Name"
	*(v[2].(*string)) = "site"
	*(v[3].(**string)) = nil
	return nil
}
func (f *cascadeRowsFixture) Err() error { return f.iterErr }
func TestCascadeReadsCompleteOrError(t *testing.T) {
	for _, c := range []struct {
		rows cascadeRowsFixture
		want int
		fail bool
	}{
		{cascadeRowsFixture{}, 0, false}, {cascadeRowsFixture{n: 5000}, 5000, false}, {cascadeRowsFixture{n: 5001}, 0, true},
		{cascadeRowsFixture{n: 2, scanErr: fmt.Errorf("scan")}, 0, true}, {cascadeRowsFixture{n: 2, iterErr: fmt.Errorf("iteration")}, 0, true},
	} {
		out, err := readCascadeDevices(&c.rows)
		if c.fail {
			if err == nil || out != nil {
				t.Fatal("partial accepted")
			}
		} else if err != nil || out == nil || len(out) != c.want {
			t.Fatal("invalid result")
		}
	}
}
