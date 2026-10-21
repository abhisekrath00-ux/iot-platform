package googlews

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func driveSim(t *testing.T, check func(authHeader string, meta map[string]string, content string)) *DriveUploader {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.t", "expires_in": 3600, "token_type": "Bearer"})
		default: // drive upload
			if got := r.URL.Query().Get("uploadType"); got != "multipart" {
				t.Errorf("uploadType = %q, want multipart", got)
			}
			mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || !strings.HasPrefix(mt, "multipart/") {
				t.Errorf("content-type = %q, want multipart/related", r.Header.Get("Content-Type"))
				return
			}
			mr := multipart.NewReader(r.Body, params["boundary"])
			var meta map[string]string
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
			check(r.Header.Get("Authorization"), meta, content)
			json.NewEncoder(w).Encode(map[string]string{"id": "file-1", "webViewLink": "https://drive.example/file-1"})
		}
	}))
	t.Cleanup(srv.Close)
	p, err := NewProvider("cid", "csecret", "https://hex.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	p.TokenURL = srv.URL + "/token"
	p.DriveURL = srv.URL + "/upload/drive/v3/files"
	return NewDriveUploader(p, newFake())
}

func TestDriveUploadHTMLAgainstSimulator(t *testing.T) {
	var gotAuth, gotContent string
	var gotMeta map[string]string
	d := driveSim(t, func(a string, m map[string]string, c string) { gotAuth, gotMeta, gotContent = a, m, c })
	ctx := context.Background()
	if err := SaveRefreshToken(ctx, d.s, "t1", "admin@x", &Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveGrantedScopes(ctx, d.s, "t1", "admin@x", "openid "+ScopeDriveReportFile); err != nil {
		t.Fatal(err)
	}
	f, err := d.Upload(ctx, "t1", "Meter report.html", "text/html", "", []byte("<html><body>report</body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "file-1" || f.WebLink != "https://drive.example/file-1" {
		t.Fatalf("file = %+v", f)
	}
	if gotAuth != "Bearer ya29.t" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotMeta["name"] != "Meter report.html" {
		t.Fatalf("meta = %v", gotMeta)
	}
	if _, conv := gotMeta["mimeType"]; conv {
		t.Fatalf("html upload must not request conversion: %v", gotMeta)
	}
	if !strings.Contains(gotContent, "<html>") {
		t.Fatalf("content = %q", gotContent)
	}
}

func TestDriveUploadCSVConvertsToSheet(t *testing.T) {
	var gotMeta map[string]string
	d := driveSim(t, func(a string, m map[string]string, c string) { gotMeta = m })
	ctx := context.Background()
	if err := SaveRefreshToken(ctx, d.s, "t1", "admin@x", &Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveGrantedScopes(ctx, d.s, "t1", "admin@x", "openid "+ScopeDriveReportFile); err != nil {
		t.Fatal(err)
	}
	_, err := d.Upload(ctx, "t1", "Meter report", "text/csv",
		"application/vnd.google-apps.spreadsheet", []byte("bucket,energy\n2026-10-01,42"))
	if err != nil {
		t.Fatal(err)
	}
	if gotMeta["mimeType"] != "application/vnd.google-apps.spreadsheet" {
		t.Fatalf("csv upload must request sheet conversion, meta = %v", gotMeta)
	}
}

func TestDriveUploadRefusedOnPartialOrLegacyGrant(t *testing.T) {
	d := driveSim(t, func(string, map[string]string, string) { t.Error("simulator must not be called") })
	ctx := context.Background()
	if err := SaveRefreshToken(ctx, d.s, "t1", "admin@x", &Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Upload(ctx, "t1", "r.html", "text/html", "", []byte("x")); err != ErrScopeUnknown {
		t.Fatalf("legacy grant: want ErrScopeUnknown, got %v", err)
	}
	if err := SaveGrantedScopes(ctx, d.s, "t1", "admin@x", "openid "+ScopeGmailSend); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Upload(ctx, "t1", "r.html", "text/html", "", []byte("x")); !errors.Is(err, ErrScopeNotGranted) {
		t.Fatalf("partial grant: want ErrScopeNotGranted, got %v", err)
	}
}

func TestDriveUploadRejectsBadInput(t *testing.T) {
	d := driveSim(t, func(string, map[string]string, string) {})
	ctx := context.Background()
	if err := SaveRefreshToken(ctx, d.s, "t1", "admin@x", &Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveGrantedScopes(ctx, d.s, "t1", "admin@x", "openid "+ScopeDriveReportFile); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Upload(ctx, "t1", "bad\nname", "text/html", "", []byte("x")); err == nil {
		t.Fatal("newline in name must fail")
	}
	if _, err := d.Upload(ctx, "t1", "ok", "text/html", "", nil); err == nil {
		t.Fatal("empty content must fail")
	}
}
