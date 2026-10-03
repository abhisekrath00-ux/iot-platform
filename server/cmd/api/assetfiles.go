package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

const (
	assetFileMax       = 5 << 20
	assetFilesPerAsset = 50
)

var assetFileTypes = map[string]string{
	".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".txt": "text/plain", ".csv": "text/csv",
}
var assetFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()-]{0,119}$`)

// POST /v1/assets/{id}/files?name=manual.pdf  (raw body). The type comes from the extension and
// the content is checked against it, so a client cannot label a script as a PDF.
func (s *server) uploadAssetFile(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	name := r.URL.Query().Get("name")
	dot := strings.LastIndex(name, ".")
	ct := ""
	if dot > 0 {
		ct = assetFileTypes[strings.ToLower(name[dot:])]
	}
	if !assetFileName.MatchString(name) || strings.Contains(name, "..") || ct == "" {
		http.Error(w, "name must be a plain file name ending in .pdf .png .jpg .jpeg .txt or .csv", 400)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, assetFileMax+1))
	if err != nil || len(data) > assetFileMax {
		http.Error(w, "file too large (5 MB max)", 413)
		return
	}
	if len(data) == 0 || !contentMatches(ct, data) {
		http.Error(w, "file content does not match its extension", 400)
		return
	}
	tenant := auth.Tenant(r)
	var one int
	if s.st.Pool.QueryRow(r.Context(), `SELECT 1 FROM assets WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), tenant).Scan(&one) != nil {
		http.Error(w, "asset not found", 404)
		return
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM asset_files WHERE asset_id=$1 AND tenant_id=$2`, r.PathValue("id"), tenant).Scan(&n)
	if n >= assetFilesPerAsset {
		http.Error(w, "too many files on this asset", 409)
		return
	}
	sum := sha256.Sum256(data)
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO asset_files(id,tenant_id,asset_id,filename,content_type,size_bytes,sha256,data,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, tenant, r.PathValue("id"), name, ct, len(data), hex.EncodeToString(sum[:]), data, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "asset.file.upload", r.PathValue("id"), map[string]any{"file": id, "name": name, "bytes": len(data)})
	writeJSON(w, 201, map[string]any{"id": id, "filename": name, "size_bytes": len(data)})
}

func contentMatches(ct string, b []byte) bool {
	switch ct {
	case "application/pdf":
		return strings.HasPrefix(string(b[:min(len(b), 5)]), "%PDF-")
	case "image/png":
		return len(b) > 8 && string(b[1:4]) == "PNG"
	case "image/jpeg":
		return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8
	default: // text and csv: valid UTF-8 without NUL bytes
		return !strings.ContainsRune(string(b), 0) && strings.ToValidUTF8(string(b), "") == string(b)
	}
}

func (s *server) listAssetFiles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,filename,content_type,size_bytes,sha256,uploaded_by,created_at FROM asset_files WHERE asset_id=$1 AND tenant_id=$2 ORDER BY created_at DESC LIMIT 100`, r.PathValue("id"), auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, fn, ct, sh string
		var sz int
		var by *string
		var at any
		if rows.Scan(&id, &fn, &ct, &sz, &sh, &by, &at) == nil {
			out = append(out, map[string]any{"id": id, "filename": fn, "content_type": ct, "size_bytes": sz, "sha256": sh, "uploaded_by": by, "created_at": at})
		}
	}
	writeJSON(w, 200, out)
}

// Always served as an attachment with nosniff and a sandboxing CSP, never inline.
func (s *server) downloadAssetFile(w http.ResponseWriter, r *http.Request) {
	var fn, ct string
	var data []byte
	if s.st.Pool.QueryRow(r.Context(), `SELECT filename,content_type,data FROM asset_files WHERE id=$1 AND asset_id=$2 AND tenant_id=$3`, r.PathValue("fid"), r.PathValue("id"), auth.Tenant(r)).Scan(&fn, &ct, &data) != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(fn))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Write(data)
}

func (s *server) deleteAssetFile(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	t, err := s.st.Pool.Exec(r.Context(), `DELETE FROM asset_files WHERE id=$1 AND asset_id=$2 AND tenant_id=$3`, r.PathValue("fid"), r.PathValue("id"), auth.Tenant(r))
	if err != nil || t.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "asset.file.delete", r.PathValue("id"), map[string]any{"file": r.PathValue("fid")})
	w.WriteHeader(204)
}
