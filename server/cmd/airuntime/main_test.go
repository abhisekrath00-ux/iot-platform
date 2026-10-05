package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeLlama(t *testing.T, healthy *bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if !*healthy {
				w.WriteHeader(503)
				return
			}
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			if r.Header.Get("Authorization") != "" {
				t.Error("the runtime key must not reach llama-server")
			}
			io.Copy(w, r.Body)
		default:
			w.Write([]byte("llama internals"))
		}
	}))
}

func TestHealthFollowsModelState(t *testing.T) {
	ok := false
	ll := fakeLlama(t, &ok)
	defer ll.Close()
	r, _ := newRT(ll.URL, "", map[string]string{"model": "qwen3-1.7b"})
	h := httptest.NewServer(r.handler())
	defer h.Close()
	get := func(p string) int { resp, _ := http.Get(h.URL + p); resp.Body.Close(); return resp.StatusCode }
	if get("/health") != 503 || get("/ready") != 503 {
		t.Fatal("must be 503 while the model loads")
	}
	ok = true
	if get("/health") != 200 || get("/ready") != 200 || get("/version") != 200 {
		t.Fatal("must be 200 once loaded")
	}
}

func TestModelAPIIsKeyedAndAllowListed(t *testing.T) {
	ok := true
	ll := fakeLlama(t, &ok)
	defer ll.Close()
	r, _ := newRT(ll.URL, "k3y", map[string]string{"model": "m"})
	h := httptest.NewServer(r.handler())
	defer h.Close()
	do := func(path, key, body string) int {
		req, _ := http.NewRequest("POST", h.URL+path, strings.NewReader(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if do("/v1/chat/completions", "", "{}") != 401 || do("/v1/chat/completions", "wrong", "{}") != 401 {
		t.Fatal("a missing or wrong key must be refused")
	}
	if do("/v1/chat/completions", "k3y", "{}") != 200 {
		t.Fatal("the right key must pass")
	}
	if do("/v1/props", "k3y", "{}") != 404 || do("/slots", "k3y", "{}") == 200 {
		t.Fatal("only the chat and models paths are exposed")
	}
	if do("/v1/chat/completions", "k3y", strings.Repeat("x", 2<<20)) == 200 {
		t.Fatal("oversized bodies must be refused")
	}
	resp, _ := http.Get(h.URL + "/metrics")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("metrics need the key when one is set")
	}
}

func TestMetricsCountRequests(t *testing.T) {
	ok := true
	ll := fakeLlama(t, &ok)
	defer ll.Close()
	r, _ := newRT(ll.URL, "", map[string]string{})
	h := httptest.NewServer(r.handler())
	defer h.Close()
	http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader("{}"))
	resp, _ := http.Get(h.URL + "/metrics")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "airuntime_requests_total 1") || !strings.Contains(string(b), "airuntime_up 1") {
		t.Fatalf("metrics:\n%s", b)
	}
}
