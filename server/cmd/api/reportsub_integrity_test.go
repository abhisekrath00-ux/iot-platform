package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore"
)

type subStoreFixture struct {
	fail  bool
	empty bool
	calls int
}

func (f *subStoreFixture) Aggregate(_ context.Context, q tsstore.SeriesQuery) ([]report.Bucket, error) {
	f.calls++
	if f.fail && f.calls%2 == 0 {
		return nil, errors.New("private backend diagnostic")
	}
	if f.empty {
		return nil, nil
	}
	return []report.Bucket{{Start: time.Now(), Avg: 4, Min: 2, Max: 6, Sum: 8, Count: 2}}, nil
}
func TestIntegrationSubreportUnavailableNotEmpty(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-subint")
	seed(t, s, "itest-subother")
	sub := report.Definition{Metrics: []report.Metric{{DeviceID: "itest-subint-dev", PointID: "temp"}, {DeviceID: "itest-subint-dev", PointID: "other"}}, WindowHours: 24, GroupBy: "hour"}
	b, _ := json.Marshal(sub)
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('subint','itest-subint','Synthetic subreport',$1,'test-user'),('subcorrupt','itest-subint','Corrupt','{"metrics":[],"group_by":"nope"}','test-user')`, b); err != nil {
		t.Fatal(err)
	}
	def := report.Definition{Subreports: []report.SubreportRef{{ReportID: "subint"}}}
	body := func(d report.Definition) string {
		if len(d.SubSummaries) != 1 {
			t.Fatal("wrong summary count")
		}
		return d.SubSummaries[0].Body
	}
	s.ts = &subStoreFixture{fail: true}
	got := body(s.subSummaries(t.Context(), "itest-subint", def))
	if !strings.Contains(got, "could not be read") || strings.Contains(got, "no readings") || strings.Contains(got, "avg") || strings.Contains(got, "private") {
		t.Fatal(got)
	}
	s.ts = &subStoreFixture{empty: true}
	got = body(s.subSummaries(t.Context(), "itest-subint", def))
	if !strings.Contains(got, "no readings") || strings.Contains(got, "could not") {
		t.Fatal(got)
	}
	s.ts = &subStoreFixture{}
	got = body(s.subSummaries(t.Context(), "itest-subint", def))
	if !strings.Contains(got, "avg 4, min 2, max 6, sum 8 (2 readings") {
		t.Fatal(got)
	}
	got = body(s.subSummaries(t.Context(), "itest-subother", def))
	if !strings.Contains(got, "not available") || strings.Contains(got, "Synthetic") {
		t.Fatal(got)
	}
	def.Subreports[0].ReportID = "subcorrupt"
	got = body(s.subSummaries(t.Context(), "itest-subint", def))
	if !strings.Contains(got, "definition is invalid") || strings.Contains(got, "no readings") {
		t.Fatal(got)
	}
	s.st.Close()
	got = body(s.subSummaries(t.Context(), "itest-subint", def))
	if !strings.Contains(got, "availability is unknown") || strings.Contains(got, "deleted") || strings.Contains(got, "closed") {
		t.Fatal(got)
	}
}
