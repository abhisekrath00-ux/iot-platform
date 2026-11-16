package admissionoutbox

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrHTTPSExchange = errors.New("staged HTTPS admission exchange refused or uncertain; input must remain retained")

// HTTPSConfig is an explicitly supplied isolated transport configuration, not
// enrollment or authority to contact an owner service. No runtime caller uses it.
// Endpoint must be exact HTTPS host/path with no credentials/query/fragment.
// RootCAs, client certificate and server hostname validation are mandatory.
// These are separate from ReceiptTrust: TLS alone cannot delete staged rows.
type HTTPSConfig struct {
	Enabled           bool
	Endpoint          string
	RootCAs           *x509.CertPool
	ClientCertificate tls.Certificate
	Timeout           time.Duration
}

// HTTPSExchange makes one fresh HTTP/1.1 connection and ONE POST per invocation.
// No environment proxy, redirects, keepalive connection replay, HTTP/2 retry,
// application retry, automatic next request or receipt parsing occurs here. The caller
// must independently verify the returned signed bytes using pinned ReceiptTrust.
// Any error can follow a committed server admission. Never treat it as rollback.
type HTTPSExchange struct {
	endpoint    string
	roots       *x509.CertPool
	certificate tls.Certificate
	timeout     time.Duration
}

func NewHTTPSExchange(c HTTPSConfig) (*HTTPSExchange, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(c.Endpoint, "\r\n\t ") || c.RootCAs == nil || len(c.RootCAs.Subjects()) == 0 || c.Timeout < time.Second || c.Timeout > 30*time.Second || len(c.ClientCertificate.Certificate) == 0 || c.ClientCertificate.PrivateKey == nil {
		return nil, ErrHTTPSExchange
	}
	// Re-parse the keypair to validate it and detach certificate byte slices.
	// The private key object remains caller-owned: no concurrent mutation allowed.
	leaf, err := x509.ParseCertificate(c.ClientCertificate.Certificate[0])
	if err != nil {
		return nil, ErrHTTPSExchange
	}
	// tls.X509KeyPair normally validates the pair. Check with crypto.Signer here
	// as callers may construct tls.Certificate directly. No secret is serialized.
	if !certificateMatchesKey(leaf, c.ClientCertificate.PrivateKey) {
		return nil, ErrHTTPSExchange
	}
	cert := c.ClientCertificate
	cert.Certificate = make([][]byte, len(c.ClientCertificate.Certificate))
	for i, b := range c.ClientCertificate.Certificate {
		cert.Certificate[i] = append([]byte(nil), b...)
	}
	cert.Leaf = leaf
	return &HTTPSExchange{endpoint: u.String(), roots: c.RootCAs.Clone(), certificate: cert, timeout: c.Timeout}, nil
}
func (x *HTTPSExchange) AdmitOnce(ctx context.Context, it Item) ([]byte, error) {
	if x == nil {
		return nil, ErrDisabled
	}
	if ctx == nil {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Full canonical item validation happens before opening any network connection.
	r := it.Reading
	check := Receipt{Version: 1, Policy: Policy, TenantID: r.TenantID, GatewayID: r.GatewayID, DeviceID: r.DeviceID, PointID: r.PointID, EventID: r.EventID, ObservedAt: r.ObservedAt, InputSHA256: it.InputSHA256, State: "pending", New: true}
	if MatchReceipt(it, check) != nil {
		return nil, ErrEvidence
	}
	// A fresh non-shared Transport per call prevents replay on a stale reused
	// connection. POST has no GetBody and no idempotency header. DNS/TCP
	// address selection remains the standard dialer, not extra HTTP attempts.
	dialer := &net.Dialer{Timeout: x.timeout}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, TLSHandshakeTimeout: x.timeout, ResponseHeaderTimeout: x.timeout, MaxResponseHeaderBytes: 16384, TLSClientConfig: &tls.Config{RootCAs: x.roots, Certificates: []tls.Certificate{x.certificate}, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: x.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, x.endpoint, bytes.NewReader(append([]byte(nil), it.Payload...)))
	if err != nil {
		return nil, ErrHTTPSExchange
	}
	request.GetBody = nil
	request.Close = true
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	// Topic is derived from the retained canonical route, not a caller-controlled
	// destination. A future handler must bind it to authenticated client identity.
	request.Header.Set("X-HexThings-Telemetry-Topic", it.Topic)
	response, err := client.Do(request)
	if err != nil {
		return nil, ErrHTTPSExchange
	}
	defer response.Body.Close()
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || media != "application/json" || response.Header.Get("Content-Encoding") != "" || response.ContentLength > 8192 {
		return nil, ErrHTTPSExchange
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(b) == 0 || len(b) > 8192 {
		return nil, ErrHTTPSExchange
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

func certificateMatchesKey(cert *x509.Certificate, key any) bool {
	signer, ok := key.(crypto.Signer)
	if !ok {
		return false
	}
	a, e := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if e != nil {
		return false
	}
	b, e := x509.MarshalPKIXPublicKey(signer.Public())
	return e == nil && bytes.Equal(a, b)
}
