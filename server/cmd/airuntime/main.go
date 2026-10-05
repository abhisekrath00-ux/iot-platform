// Command airuntime is the thin front for the bundled local model server (llama.cpp's llama-server).
// It owns what llama-server does not: /health /ready /version /model /metrics, a bearer key on the
// model API, a short allow-list of paths, a request-size cap and request metrics. It runs next to
// llama-server in the ai-runtime container and holds no platform data or credentials.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var version = "dev"

type rt struct {
	llama   *url.URL
	key     string
	model   map[string]string
	started time.Time
	client  *http.Client

	reqs, errs, inflight, latencyMsSum atomic.Int64
	lastLatencyMs                      atomic.Int64
}

func newRT(llama, key string, model map[string]string) (*rt, error) {
	u, err := url.Parse(llama)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("LLAMA_URL %q is not a URL", llama)
	}
	return &rt{llama: u, key: key, model: model, started: time.Now(), client: &http.Client{Timeout: 2 * time.Second}}, nil
}

// loaded asks llama-server whether the model is loaded and able to serve.
func (r *rt) loaded() bool {
	resp, err := r.client.Get(r.llama.String() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == 200
}

func (r *rt) info(status string) map[string]any {
	m := map[string]any{"status": status, "uptime_seconds": int(time.Since(r.started).Seconds()),
		"requests": r.reqs.Load(), "last_latency_ms": r.lastLatencyMs.Load()}
	for k, v := range r.model {
		m[k] = v
	}
	return m
}

func (r *rt) auth(w http.ResponseWriter, req *http.Request) bool {
	if r.key == "" {
		return true
	}
	got := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(r.key)) != 1 {
		http.Error(w, "unauthorized", 401)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

var allowed = map[string]bool{"/v1/chat/completions": true, "/v1/models": true}

func (r *rt) handler() http.Handler {
	mux := http.NewServeMux()
	// /health answers 200 only when the model can serve; 503 while it loads or when it is gone.
	// /ready is the same check under the name orchestrators expect.
	probe := func(w http.ResponseWriter, _ *http.Request) {
		if r.loaded() {
			writeJSON(w, 200, r.info("ok"))
			return
		}
		writeJSON(w, 503, r.info("loading"))
	}
	mux.HandleFunc("GET /health", probe)
	mux.HandleFunc("GET /ready", probe)
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"airuntime": version, "backend": "llama.cpp llama-server"})
	})
	mux.HandleFunc("GET /model", func(w http.ResponseWriter, req *http.Request) {
		if r.auth(w, req) {
			writeJSON(w, 200, r.model)
		}
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, req *http.Request) {
		if !r.auth(w, req) {
			return
		}
		up := 0
		if r.loaded() {
			up = 1
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE airuntime_up gauge\nairuntime_up %d\n# TYPE airuntime_requests_total counter\nairuntime_requests_total %d\n"+
			"# TYPE airuntime_request_errors_total counter\nairuntime_request_errors_total %d\n# TYPE airuntime_inflight gauge\nairuntime_inflight %d\n"+
			"# TYPE airuntime_request_latency_ms_sum counter\nairuntime_request_latency_ms_sum %d\n# TYPE airuntime_uptime_seconds gauge\nairuntime_uptime_seconds %d\n",
			up, r.reqs.Load(), r.errs.Load(), r.inflight.Load(), r.latencyMsSum.Load(), int(time.Since(r.started).Seconds()))
	})
	proxy := httputil.NewSingleHostReverseProxy(r.llama)
	proxy.FlushInterval = -1 // stream tokens through as they arrive
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		r.errs.Add(1)
		http.Error(w, "model runtime unavailable", 502)
	}
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, req *http.Request) {
		if !allowed[req.URL.Path] {
			http.NotFound(w, req)
			return
		}
		if !r.auth(w, req) {
			return
		}
		if req.ContentLength > 1<<20 {
			http.Error(w, "request too large", 413)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20) // chunked bodies that lie about their size
		req.Header.Del("Authorization")                    // llama-server needs none and must not see the key
		r.reqs.Add(1)
		r.inflight.Add(1)
		t0 := time.Now()
		proxy.ServeHTTP(w, req)
		ms := time.Since(t0).Milliseconds()
		r.inflight.Add(-1)
		r.latencyMsSum.Add(ms)
		r.lastLatencyMs.Store(ms)
	})
	return mux
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	r, err := newRT(env("LLAMA_URL", "http://127.0.0.1:8081"), os.Getenv("AI_RUNTIME_KEY"), map[string]string{
		"model": env("MODEL_NAME", "unknown"), "quantization": env("MODEL_QUANT", "unknown"),
		"sha256": os.Getenv("MODEL_SHA256"), "context": env("MODEL_CONTEXT", "unknown")})
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: env("LISTEN", ":8090"), Handler: r.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	log.Printf("airuntime %s on %s -> %s", version, srv.Addr, r.llama)
	log.Fatal(srv.ListenAndServe())
}
