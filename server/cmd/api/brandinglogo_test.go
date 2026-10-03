package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"testing"
)

func pngBytes(w, h int) string {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.String()
}

func TestIntegrationBrandingLogo(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-bl")
	seed(t, s, "itest-bl2")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM tenant_branding WHERE tenant_id IN ('itest-bl','itest-bl2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-bl','itest-bl2')`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/branding/logo", s.putBrandingLogo)
	api.HandleFunc("GET /v1/branding/logo", s.getBrandingLogo)
	api.HandleFunc("DELETE /v1/branding/logo", s.deleteBrandingLogo)
	if call(api, "itest-bl", "admin", "GET", "/v1/branding/logo", "").Code != 404 {
		t.Fatal("logo before upload")
	}
	if call(api, "itest-bl", "operator", "PUT", "/v1/branding/logo", pngBytes(64, 64)).Code != 403 {
		t.Fatal("operator set the logo")
	}
	if w := call(api, "itest-bl", "admin", "PUT", "/v1/branding/logo", pngBytes(64, 64)); w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	for name, body := range map[string]string{
		"svg":       `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"html":      "<script>alert(1)</script>",
		"too small": pngBytes(4, 4),
		"too large": pngBytes(2000, 2000),
		"not image": "PNG but not really",
	} {
		if c := call(api, "itest-bl", "admin", "PUT", "/v1/branding/logo", body).Code; c != 400 && c != 413 {
			t.Fatalf("%s accepted: %d", name, c)
		}
	}
	g := call(api, "itest-bl", "viewer", "GET", "/v1/branding/logo", "")
	if g.Code != 200 || g.Header().Get("Content-Type") != "image/png" || g.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("get %d %v", g.Code, g.Header())
	}
	if call(api, "itest-bl2", "viewer", "GET", "/v1/branding/logo", "").Code != 404 {
		t.Fatal("another tenant sees the logo")
	}
	if call(api, "itest-bl", "admin", "DELETE", "/v1/branding/logo", "").Code != 204 || call(api, "itest-bl", "viewer", "GET", "/v1/branding/logo", "").Code != 404 {
		t.Fatal("delete")
	}
}
