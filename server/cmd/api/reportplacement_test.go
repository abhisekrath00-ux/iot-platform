package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Real Postgres and handler paths, no scheduler/run/delivery calls.
func TestIntegrationReportPlacementLifecycle(t *testing.T) {
	s, h := testServer(t)
	const tenant = "itest-placement"
	seed(t, s, tenant)
	ctx := t.Context()
	payload := func(position string) string {
		return fmt.Sprintf(`{"name":"Placement lifecycle","definition":{"metrics":[{"device_id":"itest-placement-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour","chart":"line","sections":[{"title":"INTRO_NOTE","body":"Synthetic integration readings only.","position":%q},{"title":"END_NOTE","body":"No delivery was requested.","page_break":true}]}}`, position)
	}
	count := func(query string) int {
		t.Helper()
		var n int
		if e := s.st.Pool.QueryRow(ctx, query, tenant).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	audits := func() int { return count(`SELECT count(*) FROM audit_log WHERE tenant_id=$1`) }
	reports := func() int { return count(`SELECT count(*) FROM reports WHERE tenant_id=$1`) }
	a, n := audits(), reports()
	if w := call(h, tenant, "admin", "POST", "/v1/reports", payload("invalid")); w.Code != 400 || audits() != a || reports() != n {
		t.Fatalf("invalid create mutated state %d %s", w.Code, w.Body)
	}
	if w := call(h, tenant, "viewer", "POST", "/v1/reports/preview", payload("invalid")); w.Code != 400 {
		t.Fatalf("invalid preview%d", w.Code)
	}
	w := call(h, tenant, "admin", "POST", "/v1/reports", payload("before_data"))
	if w.Code != 201 {
		t.Fatalf("create%d %s", w.Code, w.Body)
	}
	var created struct{ ID string }
	if e := json.Unmarshal(w.Body.Bytes(), &created); e != nil || created.ID == "" {
		t.Fatal("create ID", e)
	}
	id := created.ID
	stored := func(wantVersion int, wantPosition string) {
		t.Helper()
		var def []byte
		var version int
		if e := s.st.Pool.QueryRow(ctx, `SELECT definition,version FROM reports WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&def, &version); e != nil {
			t.Fatal(e)
		}
		var d struct{ Sections []struct{ Position string } }
		if e := json.Unmarshal(def, &d); e != nil || version != wantVersion || d.Sections[0].Position != wantPosition {
			t.Fatalf("stored v%d: %s err%v", version, def, e)
		}
	}
	stored(1, "before_data")
	request := func(method, path, body, version, who string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("If-Match", version)
		r = r.WithContext(auth.AsAssistant(ctx, who, "test-user", "admin"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	ordered := func(text, before, data, after string) {
		t.Helper()
		a, b, c := strings.Index(text, before), strings.Index(text, data), strings.Index(text, after)
		if a < 0 || b < a || c < b {
			t.Fatalf("order %s/%s/%s = %d/%d/%d", before, data, after, a, b, c)
		}
	}
	checkHTML := func(body string) {
		t.Helper()
		ordered(body, "INTRO_NOTE", "<table>", "END_NOTE")
		if !strings.Contains(body, "23.000") || !strings.Contains(body, "115.000") {
			t.Fatal("real seeded weighted summary missing")
		}
	}
	preview := call(h, tenant, "viewer", "POST", "/v1/reports/preview", payload("before_data"))
	var p struct {
		HTML string
		Rows int
	}
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &p) != nil || p.Rows < 1 {
		t.Fatalf("preview%d %s", preview.Code, preview.Body)
	}
	checkHTML(p.HTML)
	unzip := func(blob []byte) map[string]string {
		t.Helper()
		z, e := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
		if e != nil {
			t.Fatal(e)
		}
		parts := map[string]string{}
		for _, f := range z.File {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			parts[f.Name] = string(b)
		}
		return parts
	}
	for _, format := range []string{"html", "csv", "pdf", "xlsx", "docx", "pptx", "xml"} {
		w := call(h, tenant, "viewer", "GET", "/v1/reports/"+id+"/download?format="+format, "")
		if w.Code != 200 {
			t.Fatalf("%s%d %s", format, w.Code, w.Body)
		}
		body := w.Body.String()
		switch format {
		case "html":
			checkHTML(body)
		case "csv":
			ordered(body, "INTRO_NOTE", "device_id,point_id", "END_NOTE")
		case "pdf":
			ordered(body, "INTRO_NOTE", "overall", "END_NOTE")
		case "xlsx":
			ordered(unzip(w.Body.Bytes())["xl/worksheets/sheet1.xml"], "INTRO_NOTE", ">device_id<", "END_NOTE")
		case "docx":
			ordered(unzip(w.Body.Bytes())["word/document.xml"], "INTRO_NOTE", "<w:tbl>", "END_NOTE")
		case "pptx":
			parts := unzip(w.Body.Bytes())
			var slides string
			for i := 1; i <= 60; i++ {
				slides += parts["ppt/slides/slide"+strconv.Itoa(i)+".xml"]
			}
			ordered(slides, "INTRO_NOTE", "<a:tbl>", "END_NOTE")
		case "xml":
			if strings.Contains(body, "INTRO_NOTE") || strings.Contains(body, "END_NOTE") {
				t.Fatal("xml unexpectedly includes notes")
			}
		}
		if dir := os.Getenv("REPORT_PLACEMENT_FIXTURE_DIR"); dir != "" {
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, "lifecycle."+format), w.Body.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if foreign := call(h, "itest-placement-other", "viewer", "GET", "/v1/reports/"+id+"/download?format="+format, ""); foreign.Code != 404 {
			t.Fatalf("cross-tenant%s=%d", format, foreign.Code)
		}
	}
	historyCount := func() int {
		var n int
		if e := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_versions WHERE report_id=$1`, id).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	a = audits()
	if w := request("PUT", "/v1/reports/"+id, payload("invalid"), "1", tenant); w.Code != 400 || audits() != a || historyCount() != 0 {
		t.Fatalf("invalid update%d %s", w.Code, w.Body)
	}
	stored(1, "before_data")
	if w := request("PUT", "/v1/reports/"+id, payload("after_data"), "1", tenant); w.Code != 200 {
		t.Fatalf("update%d %s", w.Code, w.Body)
	}
	stored(2, "after_data")
	history := call(h, tenant, "admin", "GET", "/v1/reports/"+id+"/versions", "")
	if history.Code != 200 || !strings.Contains(history.Body.String(), `"position": "before_data"`) && !strings.Contains(history.Body.String(), `"position":"before_data"`) {
		t.Fatalf("history%d %s", history.Code, history.Body)
	}
	a = audits()
	restorePath := "/v1/reports/" + id + "/versions/1/restore"
	if w := request("POST", restorePath, "", "2", "itest-placement-other"); w.Code != 404 {
		t.Fatalf("foreign restore%d", w.Code)
	}
	if w := request("POST", restorePath, "", "1", tenant); w.Code != 409 {
		t.Fatalf("stale restore%d", w.Code)
	}
	stored(2, "after_data")
	if audits() != a || historyCount() != 1 {
		t.Fatal("refused restores mutated audit/history")
	}
	if w := request("POST", restorePath, "", "2", tenant); w.Code != 200 {
		t.Fatalf("restore%d %s", w.Code, w.Body)
	}
	stored(3, "before_data")
	restored := call(h, tenant, "viewer", "GET", "/v1/reports/"+id+"/download?format=html", "")
	if restored.Code != 200 {
		t.Fatal(restored.Code)
	}
	checkHTML(restored.Body.String())
	var runs, versions int
	if e := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_runs WHERE report_id=$1`, id).Scan(&runs); e != nil {
		t.Fatal(e)
	}
	if e := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM report_versions WHERE report_id=$1`, id).Scan(&versions); e != nil {
		t.Fatal(e)
	}
	if runs != 0 || versions != 2 {
		t.Fatalf("runs%d versions%d", runs, versions)
	}
	t.Log("persisted before_data -> guarded edit after_data -> history v1 -> guarded restore v3; seven authenticated exports, foreign404, invalid400/stale409 no writes, zero runs; telemetry summary avg23/sum115/count5")
}
