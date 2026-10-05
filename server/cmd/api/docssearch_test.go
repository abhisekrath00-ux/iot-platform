package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchDocsEndpoint(t *testing.T) {
	s := &server{}
	get := func(q string) (int, map[string]any) {
		w := httptest.NewRecorder()
		s.searchDocs(w, httptest.NewRequest("GET", "/v1/docs/search?q="+q, nil))
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		return w.Code, o
	}
	code, o := get("how+do+I+restore+a+backup")
	res, _ := o["results"].([]any)
	if code != 200 || len(res) == 0 || !strings.Contains(res[0].(map[string]any)["doc"].(string), "backup") {
		t.Fatalf("%d %v", code, o)
	}
	if res[0].(map[string]any)["heading"] == "" {
		t.Fatal("results must say which heading they came from")
	}
	if code, _ := get(""); code != 400 {
		t.Fatalf("empty query: %d", code)
	}
	if code, _ := get(strings.Repeat("a", 301)); code != 400 {
		t.Fatalf("long query: %d", code)
	}
	if code, o := get("zebra+unicorn+xylophone"); code != 200 || len(o["results"].([]any)) != 0 {
		t.Fatalf("no match should be an empty list, not an error: %d %v", code, o)
	}
	_ = http.StatusOK
}
