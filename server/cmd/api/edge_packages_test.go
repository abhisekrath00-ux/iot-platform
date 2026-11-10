package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func edgeFixture(t *testing.T) (string, edgeCatalog, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("EDGE_PACKAGE_DIR", dir)
	data := []byte("MZ installer test fixture, not an executable")
	sum := sha256.Sum256(data)
	c := edgeCatalog{Version: "0.2.0", Packages: []edgePackage{{ID: "windows-amd64-exe", Filename: "hexmon-edge-0.2.0-windows-amd64-setup.exe", OS: "windows", Arch: "amd64", Format: "exe", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}}
	os.WriteFile(filepath.Join(dir, c.Packages[0].Filename), data, 0600)
	writeEdgeFixture(t, dir, c)
	s := &server{}
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/edge-packages", s.edgePackages)
	m.HandleFunc("GET /v1/edge-packages/{package}", s.edgePackages)
	return dir, c, m
}
func writeEdgeFixture(t *testing.T, dir string, c edgeCatalog) {
	t.Helper()
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestEdgePackagesInventoryAndDownload(t *testing.T) {
	_, c, h := edgeFixture(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/edge-packages", strings.NewReader("")))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = call(h, "tenant", "viewer", "GET", "/v1/edge-packages", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), c.Packages[0].Filename) {
		t.Fatal(w.Code, w.Body)
	}
	w = call(h, "tenant", "viewer", "GET", "/v1/edge-packages/windows-amd64-exe", "")
	if w.Code != 200 || w.Header().Get("X-Checksum-SHA256") != c.Packages[0].SHA256 || !strings.Contains(w.Header().Get("Content-Disposition"), c.Packages[0].Filename) {
		t.Fatal(w.Code, w.Header())
	}
	w = call(h, "tenant", "viewer", "GET", "/v1/edge-packages/unknown", "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestEdgePackagesRefuseCorruptMissingTraversalAndSymlink(t *testing.T) {
	for _, mode := range []string{"corrupt", "missing", "traversal", "symlink", "duplicate", "badarch", "badformat"} {
		t.Run(mode, func(t *testing.T) {
			dir, c, h := edgeFixture(t)
			p := filepath.Join(dir, c.Packages[0].Filename)
			switch mode {
			case "corrupt":
				os.WriteFile(p, []byte(strings.Repeat("X", int(c.Packages[0].Size))), 0600)
			case "missing":
				os.Remove(p)
			case "traversal":
				c.Packages[0].Filename = "../secret"
			case "symlink":
				os.Remove(p)
				target := filepath.Join(t.TempDir(), "secret")
				os.WriteFile(target, []byte("secret"), 0600)
				if e := os.Symlink(target, p); e != nil {
					t.Skip(e)
				}
			case "duplicate":
				c.Packages = append(c.Packages, c.Packages[0])
			case "badarch":
				c.Packages[0].Arch = "386"
			case "badformat":
				c.Packages[0].Format = "env"
			}
			writeEdgeFixture(t, dir, c)
			w := call(h, "tenant", "viewer", "GET", "/v1/edge-packages/windows-amd64-exe", "")
			if w.Code != 503 {
				t.Fatal(mode, w.Code, w.Body)
			}
		})
	}
}
func TestEdgePackagesAbsentDirectoryIsUnavailable(t *testing.T) {
	t.Setenv("EDGE_PACKAGE_DIR", filepath.Join(t.TempDir(), "absent"))
	s := &server{}
	w := call(http.HandlerFunc(s.edgePackages), "tenant", "viewer", "GET", "/v1/edge-packages", "")
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestEdgePackagesRealBuiltArtifacts(t *testing.T) {
	dir := os.Getenv("TEST_EDGE_PACKAGE_DIR")
	if dir == "" {
		t.Skip("TEST_EDGE_PACKAGE_DIR not set")
	}
	t.Setenv("EDGE_PACKAGE_DIR", dir)
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	c, e := readEdgeCatalog(root)
	if e != nil {
		t.Fatal(e)
	}
	s := &server{}
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/edge-packages/{package}", s.edgePackages)
	for _, p := range c.Packages {
		w := call(m, "fixture", "admin", "GET", "/v1/edge-packages/"+p.ID, "")
		if w.Code != 200 || int64(w.Body.Len()) != p.Size {
			t.Fatal(p.ID, w.Code, w.Body.Len())
		}
		sum := sha256.Sum256(w.Body.Bytes())
		if hex.EncodeToString(sum[:]) != p.SHA256 {
			t.Fatal("download mismatch", p.ID)
		}
	}
}
