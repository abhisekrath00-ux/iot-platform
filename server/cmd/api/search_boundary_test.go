package main

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestCustomerSearchStaysDenied(t *testing.T) {
	if scopedAllows("GET", "/v1/search") {
		t.Fatal("global search offered to scoped customers")
	}
	s := &server{}
	r := httptest.NewRequest("GET", "/v1/search?q=meter", nil)
	r = r.WithContext(context.WithValue(r.Context(), scopeKey{}, "customer"))
	w := httptest.NewRecorder()
	s.searchAll(w, r)
	if w.Code != 403 {
		t.Fatalf("handler defense: %d", w.Code)
	}
}
func TestGlobalSearchInputAndUnavailable(t *testing.T) {
	for _, c := range []struct {
		q    string
		want int
	}{{"", 400}, {"meter", 503}} {
		w := httptest.NewRecorder()
		(&server{}).searchAll(w, httptest.NewRequest("GET", "/v1/search?q="+c.q, nil))
		if w.Code != c.want {
			t.Fatalf("%d", w.Code)
		}
	}
}
