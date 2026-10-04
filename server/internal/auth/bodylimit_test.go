package auth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBodyLimit(t *testing.T) {
	read := BodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "too big", 413)
			return
		}
		w.WriteHeader(204)
	}))
	do := func(method, path string, n int, chunked bool) int {
		req := httptest.NewRequest(method, path, bytes.NewReader(make([]byte, n)))
		if chunked {
			req.ContentLength = -1 // unknown length: only the reader limit can stop it
		}
		w := httptest.NewRecorder()
		read.ServeHTTP(w, req)
		return w.Code
	}
	for _, c := range []struct {
		name         string
		method, path string
		n            int
		chunked      bool
		want         int
	}{
		{"small json", "POST", "/v1/rules", 1000, false, 204},
		{"1.5 MiB json declared", "POST", "/v1/rules", 3 << 19, false, 413},
		{"1.5 MiB json undeclared", "POST", "/v1/rules", 3 << 19, true, 413},
		{"4 MiB csv import", "POST", "/v1/telemetry/import", 4 << 20, false, 204},
		{"7 MiB csv import", "POST", "/v1/telemetry/import", 7 << 20, true, 413},
		{"5 MiB asset file", "POST", "/v1/assets/a1/files", 5 << 20, false, 204},
		{"upload path with another verb", "PUT", "/v1/telemetry/import", 2 << 20, true, 413},
		{"a lookalike path is not an upload", "POST", "/v1/assets/a1/notes", 2 << 20, false, 413},
		{"GET without body", "GET", "/v1/devices", 0, false, 204},
	} {
		if got := do(c.method, c.path, c.n, c.chunked); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
