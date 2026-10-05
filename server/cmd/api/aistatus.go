package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// aiTimeout is how long one model request may take. A CPU-only model on a small machine can need
// minutes, so operators can raise it; the whole run is still bounded by assistantTimeout.
func aiTimeout() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("AI_TIMEOUT_SECONDS")); err == nil && n >= 10 && n <= 600 {
		return time.Duration(n) * time.Second
	}
	return 90 * time.Second
}

// assistantTimeout bounds one whole assistant run (all model calls and tools). Three minutes suits
// a hosted model; a CPU-only model on a small machine needs more, so AI_RUN_TIMEOUT_SECONDS
// (30-900) raises it.
func assistantTimeout() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("AI_RUN_TIMEOUT_SECONDS")); err == nil && n >= 30 && n <= 900 {
		return time.Duration(n) * time.Second
	}
	return 3 * time.Minute
}

// isLocalRuntime is true for a model server on this host or a private network, such as the bundled
// ai-runtime. Only those get the llama.cpp-specific request fields.
func isLocalRuntime(base string) bool {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "ai-runtime" || h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// GET /v1/ai/status: is the AI layer configured and is the model runtime answering? Any signed-in
// user may ask (the chat panel shows a status dot); it reveals no endpoint address or key. The
// platform itself never depends on this answer.
func (s *server) aiStatus(w http.ResponseWriter, r *http.Request) {
	tenant := auth.Tenant(r)
	c, ok := s.aiConfigFor(r.Context(), tenant)
	out := map[string]any{"configured": ok && c.Enabled && c.BaseURL != "" && c.Model != "", "model": c.Model, "state": "not_configured"}
	if out["configured"] == true {
		state, info := probeRuntime(r.Context(), c.BaseURL)
		out["state"] = state
		for k, v := range info {
			out[k] = v
		}
	}
	writeJSON(w, 200, out)
}

// probeRuntime asks the runtime's /health (the bundled sidecar and llama-server both answer it).
// Hosted endpoints that have no /health are reported "unknown", not "down".
func probeRuntime(ctx context.Context, base string) (string, map[string]any) {
	if llm.ValidateBaseURL(base) != nil {
		return "error", nil
	}
	u, _ := url.Parse(strings.TrimSpace(base))
	hu := *u
	hu.Path, hu.RawQuery = "/health", ""
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", hu.String(), nil)
	resp, err := (&http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		if isLocalRuntime(base) {
			return "down", nil
		}
		return "unreachable", nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == 200:
		var info map[string]any
		json.Unmarshal(raw, &info)
		keep := map[string]any{}
		for _, k := range []string{"model", "quantization", "context", "uptime_seconds", "requests", "last_latency_ms"} {
			if v, ok := info[k]; ok {
				keep["runtime_"+k] = v
			}
		}
		return "ready", keep
	case resp.StatusCode == 503:
		return "loading", nil
	}
	return "unknown", nil
}
