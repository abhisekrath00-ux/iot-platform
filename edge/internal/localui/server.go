package localui

import (
	"context"
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"time"
)

//go:embed index.html
var indexHTML []byte

// Info is the static and live gateway facts the page shows.
type Info struct {
	GatewayID  string
	TenantID   string
	Version    string
	BrokerHost string
	BrokerTLS  bool
	// Live probes, called per request. Must be cheap and non-blocking.
	Connected  func() bool
	QueueDepth func(context.Context) (int, error)
}

type statusDoc struct {
	GatewayID  string         `json:"gateway_id"`
	TenantID   string         `json:"tenant_id"`
	Version    string         `json:"version"`
	Broker     string         `json:"broker"`
	BrokerTLS  bool           `json:"broker_tls"`
	Connected  bool           `json:"connected"`
	QueueDepth int            `json:"queue_depth"`
	UptimeSec  int64          `json:"uptime_seconds"`
	Now        time.Time      `json:"now"`
	Devices    []DeviceStatus `json:"devices"`
}

// Handler builds the read-only mux. GET/HEAD only.
func Handler(info Info, t *Tracker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		started, devs := t.Snapshot()
		doc := statusDoc{
			GatewayID: info.GatewayID, TenantID: info.TenantID, Version: info.Version,
			Broker: info.BrokerHost, BrokerTLS: info.BrokerTLS,
			UptimeSec: int64(time.Since(started).Seconds()), Now: time.Now().UTC(), Devices: devs,
		}
		if info.Connected != nil {
			doc.Connected = info.Connected()
		}
		if info.QueueDepth != nil {
			if n, err := info.QueueDepth(r.Context()); err == nil {
				doc.QueueDepth = n
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	return secure(readOnly(mux))
}

func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Serve listens on addr until ctx ends. Non-loopback binds are allowed only
// because the operator chose them explicitly in config; the page is read-only.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
