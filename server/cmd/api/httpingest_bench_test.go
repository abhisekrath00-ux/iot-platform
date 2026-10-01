package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// BenchmarkHTTPIngest measures the handler plus Postgres on the machine running
// the test: one batch of 100 distinct readings per op. Run with
// go test ./cmd/api -run xxx -bench HTTPIngest -benchtime 200x
func BenchmarkHTTPIngest(b *testing.B) {
	s, _ := testServer(b)
	seed(b, s, "bench-hi")
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/telemetry/ingest", s.ingestHTTP)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sb strings.Builder
		for j := 0; j < 100; j++ {
			if j > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"point":"temp","value":%d,"event_id":"b-%d-%d"}`, 20+j%50, i, j)
		}
		w := call(api, "bench-hi", "operator", "POST", "/v1/telemetry/ingest", `{"device_id":"bench-hi-dev","readings":[`+sb.String()+`]}`)
		if w.Code != 200 {
			b.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	b.ReportMetric(float64(b.N*100)/b.Elapsed().Seconds(), "readings/s")
}
