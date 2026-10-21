package main

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

func driveReq(t *testing.T, method, path, body, role string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetPathValue("id", "rep-dx1")
	c := context.WithValue(r.Context(), auth.CtxTenant, "itest-dx")
	c = context.WithValue(c, auth.CtxUser, "itest-dx-admin")
	c = context.WithValue(c, auth.CtxRole, role)
	return r.WithContext(c)
}

// Export to Drive uploads the rendered report (HTML) and the CSV->Sheet
// conversion, records each export, and answers honestly when the deployment
// or tenant is not connected. Google simulator throughout. Requires
// TEST_DATABASE_URL (CI); skips locally.
func TestIntegrationReportDriveExport(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-dx")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: key}
	ctx := context.Background()

	var uploads []map[string]string // meta per upload
	sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && !strings.Contains(r.URL.RawQuery, "uploadType") { // token endpoint
			json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.sim", "expires_in": 3600, "token_type": "Bearer"})
			return
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		meta := map[string]string{}
		var content string
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(part)
			if strings.Contains(part.Header.Get("Content-Type"), "application/json") {
				_ = json.Unmarshal(b, &meta)
			} else {
				content = string(b)
			}
		}
		meta["__content"] = content
		uploads = append(uploads, meta)
		json.NewEncoder(w).Encode(map[string]string{"id": "drv-1", "webViewLink": "https://drive.example/drv-1"})
	}))
	t.Cleanup(sim.Close)
	p, _ := googlews.NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	p.TokenURL = sim.URL
	p.DriveURL = sim.URL + "/upload/drive/v3/files"
	s.google = p
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM reports WHERE tenant_id='itest-dx'`,
			`DELETE FROM report_drive_exports WHERE tenant_id='itest-dx'`,
			`DELETE FROM secrets WHERE tenant_id='itest-dx'`,
			`DELETE FROM audit_log WHERE tenant_id='itest-dx'`,
		} {
			s.st.Pool.Exec(ctx, q)
		}
	})
	s.st.Pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('rep-dx1','itest-dx','Daily temps','{"metrics":[{"device_id":"itest-dx-dev","point_id":"temp"}],"window_hours":1,"group_by":"hour"}'::jsonb,'test-user')`)

	// viewer cannot export
	rec := httptest.NewRecorder()
	s.exportReportDrive(rec, driveReq(t, "POST", "/v1/reports/rep-dx1/export/drive", `{"format":"html"}`, "viewer"))
	if rec.Code != 403 {
		t.Fatalf("viewer: %d", rec.Code)
	}
	// tenant without a Google grant gets the honest 409, no Drive call
	rec = httptest.NewRecorder()
	s.exportReportDrive(rec, driveReq(t, "POST", "/v1/reports/rep-dx1/export/drive", `{"format":"html"}`, "admin"))
	if rec.Code != 409 || len(uploads) != 0 {
		t.Fatalf("unconnected: %d uploads=%d", rec.Code, len(uploads))
	}
	if err := googlews.SaveRefreshToken(ctx, s.secrets, "itest-dx", "admin@x", &googlews.Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	if err := googlews.SaveGrantedScopes(ctx, s.secrets, "itest-dx", "admin@x", "openid "+googlews.ScopeDriveReportFile); err != nil {
		t.Fatal(err)
	}
	// bad format
	rec = httptest.NewRecorder()
	s.exportReportDrive(rec, driveReq(t, "POST", "/v1/reports/rep-dx1/export/drive", `{"format":"pdf"}`, "admin"))
	if rec.Code != 400 {
		t.Fatalf("bad format: %d", rec.Code)
	}
	// html export: stored as-is, no conversion requested
	rec = httptest.NewRecorder()
	s.exportReportDrive(rec, driveReq(t, "POST", "/v1/reports/rep-dx1/export/drive", `{"format":"html"}`, "admin"))
	if rec.Code != 200 {
		t.Fatalf("html export: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["web_link"] != "https://drive.example/drv-1" {
		t.Fatalf("html export out: %v", out)
	}
	if len(uploads) != 1 || !strings.HasSuffix(uploads[0]["name"], ".html") {
		t.Fatalf("html upload meta: %v", uploads)
	}
	if _, conv := uploads[0]["mimeType"]; conv {
		t.Fatalf("html must not convert: %v", uploads[0])
	}
	if !strings.Contains(uploads[0]["__content"], "<html") && !strings.Contains(uploads[0]["__content"], "<!") {
		t.Fatalf("html content: %.120q", uploads[0]["__content"])
	}
	// sheet export: CSV uploaded with spreadsheet conversion
	rec = httptest.NewRecorder()
	s.exportReportDrive(rec, driveReq(t, "POST", "/v1/reports/rep-dx1/export/drive", `{"format":"sheet"}`, "admin"))
	if rec.Code != 200 {
		t.Fatalf("sheet export: %d %s", rec.Code, rec.Body.String())
	}
	if len(uploads) != 2 || uploads[1]["mimeType"] != "application/vnd.google-apps.spreadsheet" {
		t.Fatalf("sheet upload meta: %v", uploads[1])
	}
	if !strings.Contains(uploads[1]["__content"], ",") {
		t.Fatalf("sheet content must be csv: %.120q", uploads[1]["__content"])
	}
	// export history lists both, newest first
	rec = httptest.NewRecorder()
	s.listReportExports(rec, driveReq(t, "GET", "/v1/reports/rep-dx1/exports", "", "viewer"))
	var hist struct {
		Exports []struct {
			Format  string `json:"format"`
			WebLink string `json:"web_link"`
			Status  string `json:"status"`
		} `json:"exports"`
	}
	json.Unmarshal(rec.Body.Bytes(), &hist)
	if len(hist.Exports) != 2 || hist.Exports[0].Format != "sheet" || hist.Exports[1].Format != "html" || hist.Exports[0].Status != "exported" {
		t.Fatalf("history: %+v", hist.Exports)
	}
	// no Google configuration at all: 503, not a crash
	s.google = nil
	rec = httptest.NewRecorder()
	s.exportReportDrive(rec, driveReq(t, "POST", "/v1/reports/rep-dx1/export/drive", `{"format":"html"}`, "admin"))
	if rec.Code != 503 {
		t.Fatalf("unconfigured: %d", rec.Code)
	}
}
