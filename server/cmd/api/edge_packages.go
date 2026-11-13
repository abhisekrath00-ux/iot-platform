package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

type edgePackage struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Format   string `json:"format"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}
type edgeCatalog struct {
	Version  string        `json:"version"`
	Packages []edgePackage `json:"packages"`
}

var edgeVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`)
var edgeDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func readEdgeCatalog(root *os.Root) (edgeCatalog, error) {
	var c edgeCatalog
	f, err := root.Open("manifest.json")
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("trailing catalog content")
	}
	if !edgeVersion.MatchString(c.Version) || len(c.Packages) == 0 || len(c.Packages) > 20 {
		return c, fmt.Errorf("invalid catalog")
	}
	ids := map[string]bool{}
	for _, p := range c.Packages {
		if (p.OS != "windows" && p.OS != "linux") || (p.Arch != "amd64" && p.Arch != "arm64") || p.Size <= 0 || p.Size > 100<<20 || !edgeDigest.MatchString(p.SHA256) {
			return c, fmt.Errorf("invalid package")
		}
		formats := map[string]bool{"run": true, "deb": true, "tar.gz": true}
		if p.OS == "windows" {
			formats = map[string]bool{"exe": true, "zip": true}
		}
		if !formats[p.Format] || p.ID != p.OS+"-"+p.Arch+"-"+strings.ReplaceAll(p.Format, ".", "-") || ids[p.ID] {
			return c, fmt.Errorf("invalid package identity")
		}
		ids[p.ID] = true
		name := "hexmon-edge-" + c.Version + "-" + p.OS + "-" + p.Arch
		if p.Format == "exe" {
			name += "-setup"
		}
		name += "." + p.Format
		if p.Format == "deb" {
			name = "hexmon-edge_" + strings.TrimPrefix(c.Version, "v") + "_" + p.Arch + ".deb"
		}
		if p.Filename != name {
			return c, fmt.Errorf("invalid package filename")
		}
		stat, e := root.Lstat(name)
		if e != nil || !stat.Mode().IsRegular() || stat.Size() != p.Size {
			return c, fmt.Errorf("missing or changed package")
		}
	}
	return c, nil
}
func (s *server) edgePackages(w http.ResponseWriter, r *http.Request) {
	if auth.Tenant(r) == "" {
		http.Error(w, "authentication required", 401)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	root, err := os.OpenRoot(envOr("EDGE_PACKAGE_DIR", "/edge-packages"))
	if err != nil {
		http.Error(w, "Edge installers unavailable on this server. Rebuild the server image with bundled edge packages.", 503)
		return
	}
	defer root.Close()
	c, err := readEdgeCatalog(root)
	if err != nil {
		http.Error(w, "Edge installer inventory is incomplete or invalid. Rebuild the server image; no fallback download is used.", 503)
		return
	}
	id := r.PathValue("package")
	if id == "" {
		writeJSON(w, 200, c)
		return
	}
	for _, p := range c.Packages {
		if p.ID == id {
			f, e := root.Open(p.Filename)
			if e != nil {
				http.Error(w, "Package unavailable", 503)
				return
			}
			defer f.Close()
			data, e := io.ReadAll(io.LimitReader(f, p.Size+1))
			sum := sha256.Sum256(data)
			if e != nil || int64(len(data)) != p.Size || hex.EncodeToString(sum[:]) != p.SHA256 {
				http.Error(w, "Package integrity check failed. Rebuild server packages before retrying.", 503)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+p.Filename+`"`)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Checksum-SHA256", p.SHA256)
			w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}
