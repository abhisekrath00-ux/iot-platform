package auth

import (
	"net/http"
	"strings"
)

const (
	// DefaultBodyMax caps every request body. Handlers apply their own, smaller caps where they know better;
	// this is the backstop for the ones that decode JSON without one.
	DefaultBodyMax = 1 << 20
	// UploadBodyMax is the ceiling for the few endpoints that take real uploads (telemetry CSV import up to
	// 4 MiB, asset files up to 5 MiB). Those handlers still enforce their own exact limits below this.
	UploadBodyMax = 6 << 20
)

func isUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	return p == "/v1/telemetry/import" || (strings.HasPrefix(p, "/v1/assets/") && strings.HasSuffix(p, "/files"))
}

// BodyLimit bounds how much of a request body any handler can be made to read.
func BodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			max := int64(DefaultBodyMax)
			if isUpload(r) {
				max = UploadBodyMax
			}
			if r.ContentLength > max {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}
