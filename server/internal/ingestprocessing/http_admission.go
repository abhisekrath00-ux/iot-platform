package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

var ErrAdmissionHTTP = errors.New("staged admission HTTP requires explicit engine, signer and certificate route bindings")

// CertificateRoute is an explicit administrator-supplied full leaf certificate
// SHA256 binding, not a CN, forwarded header, discovery result or enrollment.
// One certificate authorizes one exact tenant/gateway topic. Rotation/revocation
// requires separately authorized configuration; this handler also supports a
// deny-only process-local fence, not a mutable enrollment endpoint.
type CertificateRoute struct {
	LeafSHA256 [32]byte
	Topic      string
}

type AdmissionHTTPConfig struct {
	Enabled bool
	Engine  Engine
	Signing ReceiptSigning
	Path    string
	Routes  []CertificateRoute
	Timeout time.Duration
	// DurableDenials is separate explicit staging, requires exact authority95.
	// False preserves historical process-local-only behavior, NOT operational safety.
	DurableDenials bool
}

// AdmissionHTTP is an opt-in handler only: no production route/listener/startup
// uses it. A future dedicated server MUST require verified client certificates,
// set header/idle timeouts and header limits, and protect the signing key. This
// handler separately checks verified TLS leaf and exact frozen certificate route.
// It never trusts proxy authentication headers and must not sit behind TLS
// termination. It signs only the real transaction's committed/exact-replay result.
// A response error can follow commit; it is NOT rollback or permission to retry
// external effects. There is no derived execution, MQTT ACK or internal retry.
type AdmissionHTTP struct {
	engine         Engine
	signing        ReceiptSigning
	path           string
	routes         map[[32]byte]string
	timeout        time.Duration
	routeMu        sync.Mutex
	fences         map[[32]byte]*routeFence
	durableDenials bool
}

func NewAdmissionHTTP(c AdmissionHTTPConfig) (*AdmissionHTTP, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if !c.Engine.Enabled || c.Engine.Pool == nil || c.Timeout < time.Second || c.Timeout > 30*time.Second || len(c.Routes) == 0 || len(c.Routes) > 256 || c.Path != "/staged/processing-v1/admit" {
		return nil, ErrAdmissionHTTP
	}
	key, err := receiptSigningKey(c.Signing)
	if err != nil {
		return nil, err
	}
	routes := make(map[[32]byte]string, len(c.Routes))
	for _, r := range c.Routes {
		if r.LeafSHA256 == ([32]byte{}) || !validHTTPTopic(r.Topic) {
			return nil, ErrAdmissionHTTP
		}
		if _, exists := routes[r.LeafSHA256]; exists {
			return nil, ErrAdmissionHTTP
		}
		routes[r.LeafSHA256] = r.Topic
	}
	signing := c.Signing
	signing.PrivateKey = key
	fences := make(map[[32]byte]*routeFence, len(routes))
	for leaf := range routes {
		fences[leaf] = &routeFence{drained: make(chan struct{})}
	}
	return &AdmissionHTTP{durableDenials: c.DurableDenials, fences: fences, engine: c.Engine, signing: signing, path: c.Path, routes: routes, timeout: c.Timeout}, nil
}
func validHTTPTopic(s string) bool {
	// Reuse the exact wire parser, with a synthetic stable canonical input. No DB.
	_, err := decodeMQTT(s, []byte(`{"event_id":"validation","device_id":"validation","point_id":"validation","observed_at":"2026-01-01T00:00:00Z","value":0,"quality":"measured","schema_version":1}`))
	return err == nil
}
func (h *AdmissionHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	refuse := func(code int) { http.Error(w, "staged admission refused or uncertain; retain original input", code) }
	if h == nil {
		refuse(http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost || r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.URL.Path != h.path || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
		refuse(http.StatusBadRequest)
		return
	}
	state := r.TLS
	if state == nil || !state.HandshakeComplete || len(state.PeerCertificates) == 0 || len(state.VerifiedChains) == 0 {
		refuse(http.StatusForbidden)
		return
	}
	leaf := state.PeerCertificates[0]
	verified := false
	for _, chain := range state.VerifiedChains {
		if len(chain) > 0 && bytes.Equal(chain[0].Raw, leaf.Raw) {
			verified = true
			break
		}
	}
	if !verified {
		refuse(http.StatusForbidden)
		return
	}
	topic, ok := h.routes[sha256.Sum256(leaf.Raw)]
	if !ok {
		refuse(http.StatusForbidden)
		return
	}
	topics := r.Header.Values("X-HexThings-Telemetry-Topic")
	if len(topics) != 1 || topics[0] != topic {
		refuse(http.StatusForbidden)
		return
	}
	types := r.Header.Values("Content-Type")
	if len(types) != 1 || len(r.Header.Values("Content-Encoding")) != 0 || r.ContentLength > 8192 {
		refuse(http.StatusBadRequest)
		return
	}
	media, _, err := mime.ParseMediaType(types[0])
	if err != nil || media != "application/json" {
		refuse(http.StatusBadRequest)
		return
	}
	// Bound slow body and response writes on the actual connection. Unsupported
	// wrappers refuse rather than pretending a context cancels a blocked body read.
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(h.timeout)
	if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline) != nil {
		refuse(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	b, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(b) == 0 || len(b) > 8192 {
		refuse(http.StatusBadRequest)
		return
	}
	if _, err = decodeMQTT(topic, b); err != nil {
		refuse(http.StatusBadRequest)
		return
	}
	// This fence is immediately before DB admission and remains held logically
	// through response completion. Earlier body reads do not reserve route access.
	leafID := sha256.Sum256(leaf.Raw)
	if h.beginRoute(leafID) != nil {
		refuse(http.StatusForbidden)
		return
	}
	defer h.endRoute(leafID)
	engine := h.engine
	if h.durableDenials {
		engine.durableLeaf = &leafID
	}
	wire, err := engine.AdmitSignedMQTT(ctx, topic, b, h.signing)
	if err != nil || ctx.Err() != nil {
		refuse(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// A short/failed write is uncertain. Never re-admit or execute on this path.
	_, _ = w.Write(wire)
}
