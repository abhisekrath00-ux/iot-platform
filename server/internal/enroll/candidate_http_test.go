package enroll

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCandidateHTTPRefusesBeforeDatabase(t *testing.T) {
	c := CandidateAttempts{}
	h := c.CandidateHandler(false, nil)
	for _, body := range []string{`{}`, `{"serial":"a","serial":"b"}`, `{"Serial":"a"}`, `{"extra":true}`, `{"claim_code":{}}`, `{} {}`, strings.Repeat("x", 24577)} {
		r := httptest.NewRequest("POST", "https://localhost/candidate/claim", strings.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("invalid accepted")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "https://localhost/candidate/claim", strings.NewReader(`{}`)).WithContext(ctx)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cancelled request")
	}
}
