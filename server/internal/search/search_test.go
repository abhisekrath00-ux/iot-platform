package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQueryBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"valid", 200, `{"hits":{"hits":[{"_source":{"tenant_id":"a","id":"1"}}]}}`, true},
		{"empty", 200, `{"hits":{"hits":[]}}`, true},
		{"backend error", 503, `{"error":"secret diagnostics"}`, false},
		{"bad json", 200, `{`, false},
		{"not search shape", 200, `{}`, false},
		{"other tenant", 200, `{"hits":{"hits":[{"_source":{"tenant_id":"b"}}]}}`, false},
		{"missing tenant", 200, `{"hits":{"hits":[{"_source":{"id":"1"}}]}}`, false},
		{"too large", 200, strings.Repeat("x", maxSearchResponse+1), false}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				filter := b["query"].(map[string]any)["bool"].(map[string]any)["filter"].(map[string]any)
				if filter["term"].(map[string]any)["tenant_id"] != "a" {
					t.Error("tenant filter absent")
				}
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			}))
			defer hs.Close()
			hits, err := New(hs.URL).Query(context.Background(), []string{"devices"}, "a", "meter")
			if (err == nil) != c.ok {
				t.Fatalf("hits=%s err=%v", hits, err)
			}
			if c.name == "empty" && hits == nil {
				t.Fatal("empty hits must be []")
			}
		})
	}
}
func TestQueryRedirectAndInputs(t *testing.T) {
	reached := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))
	defer target.Close()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer hs.Close()
	c := New(hs.URL)
	if _, e := c.Query(context.Background(), []string{"devices"}, "a", "q"); e == nil || reached != 0 {
		t.Fatal("redirect followed")
	}
	for _, indices := range [][]string{nil, {"../secrets"}, {"devices?x"}} {
		if _, e := c.Query(context.Background(), indices, "a", "q"); e == nil {
			t.Fatal("invalid index accepted")
		}
	}
	for _, q := range []string{"", strings.Repeat("x", 513)} {
		if _, e := c.Query(context.Background(), []string{"devices"}, "a", q); e == nil {
			t.Fatal("invalid query accepted")
		}
	}
	if _, e := c.Query(context.Background(), []string{"devices"}, "", "q"); e == nil {
		t.Fatal("empty tenant accepted")
	}
	if e := New(":bad").Index(context.Background(), "devices", "id", map[string]any{}); e == nil {
		t.Fatal("malformed URL accepted")
	}
	if e := c.Index(context.Background(), "devices", "id", make(chan int)); e == nil {
		t.Fatal("marshal error ignored")
	}
}
