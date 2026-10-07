package main

import (
	"context"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"net/http"
	"testing"
)

func TestReportRowCap(t *testing.T) {
	t.Setenv("REPORT_MAX_ROWS", "")
	if reportRowCap() != 200000 || checkRowCap(200000) != nil || checkRowCap(200001) == nil {
		t.Fatal("default cap")
	}
	t.Setenv("REPORT_MAX_ROWS", "10")
	if checkRowCap(10) != nil || checkRowCap(11) != errReportTooLarge {
		t.Fatal("env cap")
	}
	t.Setenv("REPORT_MAX_ROWS", "-5")
	if reportRowCap() != 200000 {
		t.Fatal("bad value must fall back")
	}
	if reportErrStatus(fmt.Errorf("wrap: %w", errReportTooLarge)) != http.StatusRequestEntityTooLarge || reportErrStatus(fmt.Errorf("x")) != 500 {
		t.Fatal("status mapping")
	}
}

func TestIntegrationReportRowCapEndToEnd(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cap")
	def := report.Definition{Metrics: []report.Metric{{DeviceID: "itest-cap-dev", PointID: "temp"}}, WindowHours: 24, GroupBy: "15min"}
	if _, total, err := s.buildSeries(context.Background(), "itest-cap", def); err != nil || total == 0 {
		t.Fatalf("uncapped: %d %v", total, err)
	}
	t.Setenv("REPORT_MAX_ROWS", "1")
	def.Metrics = append(def.Metrics, def.Metrics[0]) // same metric twice doubles the rows read
	if _, _, err := s.buildSeries(context.Background(), "itest-cap", def); err != errReportTooLarge {
		t.Fatalf("want errReportTooLarge, got %v", err)
	}
}
