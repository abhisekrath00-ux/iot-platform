package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// Opt-in, actual build artifacts through TLS loopback + JWT + production
// download handler. Does not install an EXE, exercise full session revocation,
// prove code signing/provenance, or replace a real dashboard/browser download.
func TestRealBuiltEdgePackagesDownload(t *testing.T) {
	source := os.Getenv("TEST_EDGE_PACKAGE_DIR")
	if source == "" {
		t.Skip("TEST_EDGE_PACKAGE_DIR required: build all ten artifacts first")
	}
	expectedVersion := os.Getenv("TEST_EDGE_PACKAGE_VERSION")
	if expectedVersion == "" {
		t.Fatal("explicit TEST_EDGE_PACKAGE_VERSION required")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected edgeCatalog
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Version != expectedVersion || len(expected.Packages) != 10 {
		t.Fatalf("wrong build/version inventory: %s count%d", expected.Version, len(expected.Packages))
	}
	// Private copies: fault injection MUST NOT change the real build directory.
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range expected.Packages {
		if filepath.Base(p.Filename) != p.Filename {
			t.Fatal("unsafe manifest")
		}
		data, e := os.ReadFile(filepath.Join(source, p.Filename))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, p.Filename), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("EDGE_PACKAGE_DIR", dir)
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/edge-packages", s.edgePackages)
	mux.HandleFunc("GET /v1/edge-packages/{package}", s.edgePackages)
	secret := []byte("synthetic-real-package-loopback-only")
	srv := httptest.NewTLSServer(auth.Middleware(secret)(mux))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 15 * time.Second
	token := func(tenant string, expiry time.Time) string {
		v, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: tenant, Role: "viewer", RegisteredClaims: jwt.RegisteredClaims{Subject: "synthetic-package-viewer", ExpiresAt: jwt.NewNumericDate(expiry)}}).SignedString(secret)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	good := token("synthetic-package-tenant", time.Now().Add(5*time.Minute))
	get := func(path, bearer string) (int, http.Header, []byte) {
		t.Helper()
		r, e := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if e != nil {
			t.Fatal(e)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		body, e := io.ReadAll(io.LimitReader(resp.Body, 101<<20))
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, resp.Header, body
	}
	for _, credential := range []string{"", "not-a-jwt", token("synthetic-package-tenant", time.Now().Add(-time.Minute)), token("", time.Now().Add(time.Minute))} {
		for _, path := range []string{"/v1/edge-packages", "/v1/edge-packages/windows-amd64-exe"} {
			code, _, _ := get(path, credential)
			if code != 401 {
				t.Fatalf("unauthorized %s got%d", path, code)
			}
		}
	}
	code, headers, body := get("/v1/edge-packages", good)
	if code != 200 || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("inventory%d %s", code, body)
	}
	var served edgeCatalog
	if err = json.Unmarshal(body, &served); err != nil {
		t.Fatal(err)
	}
	if served.Version != expectedVersion || len(served.Packages) != 10 {
		t.Fatal("served inventory changed")
	}
	seen := map[string]bool{}
	for _, p := range served.Packages {
		if seen[p.ID] {
			t.Fatal("duplicate package", p.ID)
		}
		seen[p.ID] = true
		original, e := os.ReadFile(filepath.Join(source, p.Filename))
		if e != nil {
			t.Fatal(e)
		}
		code, h, download := get("/v1/edge-packages/"+p.ID, good)
		sum := sha256.Sum256(download)
		if code != 200 || int64(len(download)) != p.Size || hex.EncodeToString(sum[:]) != p.SHA256 || !bytes.Equal(original, download) {
			t.Fatalf("changed download %s code%d", p.ID, code)
		}
		if h.Get("X-Checksum-SHA256") != p.SHA256 || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" || h.Get("Content-Disposition") != `attachment; filename="`+p.Filename+`"` {
			t.Fatal("unsafe download headers", p.ID, h)
		}
		// Inspect real machine types and archive contents, not a fake MZ prefix.
		switch p.Format {
		case "exe":
			verifyPackagePE(t, download, p.Arch)
		case "zip":
			z, e := zip.NewReader(bytes.NewReader(download), int64(len(download)))
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, f := range z.File {
				if strings.HasSuffix(f.Name, "/edge-agent.exe") {
					r, e := f.Open()
					if e != nil {
						t.Fatal(e)
					}
					b, e := io.ReadAll(io.LimitReader(r, 100<<20))
					r.Close()
					if e != nil {
						t.Fatal(e)
					}
					verifyPackagePE(t, b, p.Arch)
					found = true
				}
			}
			if !found {
				t.Fatal("agent absent", p.ID)
			}
		case "tar.gz":
			gz, e := gzip.NewReader(bytes.NewReader(download))
			if e != nil {
				t.Fatal(e)
			}
			tr := tar.NewReader(gz)
			found := false
			for {
				h, e := tr.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				if strings.HasSuffix(h.Name, "/edge-agent") {
					b, e := io.ReadAll(io.LimitReader(tr, 100<<20))
					if e != nil {
						t.Fatal(e)
					}
					f, e := elf.NewFile(bytes.NewReader(b))
					if e != nil {
						t.Fatal(e)
					}
					want := elf.EM_X86_64
					if p.Arch == "arm64" {
						want = elf.EM_AARCH64
					}
					if f.Machine != want || h.Mode&0111 == 0 {
						t.Fatal("wrong ELF or mode", p.ID)
					}
					f.Close()
					found = true
				}
			}
			gz.Close()
			if !found {
				t.Fatal("agent absent", p.ID)
			}
		case "run":
			if !bytes.HasPrefix(download, []byte("#!/")) {
				t.Fatal("not a self-extractor", p.ID)
			}
		case "deb":
			if !bytes.HasPrefix(download, []byte("!<arch>\n")) {
				t.Fatal("not a deb archive", p.ID)
			}
		default:
			t.Fatal("unexpected format", p.Format)
		}
	}
	if script := os.Getenv("TEST_EDGE_PACKAGE_BROWSER_SCRIPT"); script != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", script)
		cmd.Env = append(os.Environ(), "HEX_PACKAGE_TEST_API="+srv.URL, "HEX_PACKAGE_TEST_TOKEN="+good, "HEX_PACKAGE_SOURCE="+source)
		result, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("browser contract: %v\n%s", err, result)
		}
		t.Log(string(result))
	}
	// Shared packages are intentionally tenant-independent; authentication is not
	// an assertion that a download from another tenant has distinct bytes.
	code, _, other := get("/v1/edge-packages", token("other-synthetic-tenant", time.Now().Add(time.Minute)))
	if code != 200 || !bytes.Equal(other, body) {
		t.Fatal("shared package catalog mismatch")
	}
	code, _, _ = get("/v1/edge-packages/unknown", good)
	if code != 404 {
		t.Fatal(code)
	}
	selected := served.Packages[0]
	path := filepath.Join(dir, selected.Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 1
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	code, _, leak := get("/v1/edge-packages/"+selected.ID, good)
	if code != 503 || bytes.Contains(leak, data[:64]) {
		t.Fatal("corrupt executable served", code)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	code, _, _ = get("/v1/edge-packages", good)
	if code != 503 {
		t.Fatal("missing package catalog accepted")
	}
	t.Logf("actual %s ten-artifact TLS/JWT inventory+byte-identical downloads/hash/PE-ELF architectures/auth and tamper refusal PASS; no browser/native installation/physical acceptance", expectedVersion)
}
func verifyPackagePE(t *testing.T, data []byte, arch string) {
	t.Helper()
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if arch == "arm64" {
		want = pe.IMAGE_FILE_MACHINE_ARM64
	}
	if f.Machine != want || len(f.Sections) == 0 {
		t.Fatal("wrong PE architecture", arch, f.Machine)
	}
}
