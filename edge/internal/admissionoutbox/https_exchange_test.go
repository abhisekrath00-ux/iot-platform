package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type testMTLS struct {
	roots     *x509.CertPool
	client    tls.Certificate
	server    tls.Certificate
	wrongHost tls.Certificate
}

func mtlsFixture(t *testing.T) testMTLS {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(caDER)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	issue := func(n int64, client bool) tls.Certificate {
		p, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: "isolated"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		if client {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		} else {
			tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			tmpl.DNSNames = []string{"localhost"}
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		if n == 4 {
			tmpl.IPAddresses = nil
			tmpl.DNSNames = []string{"not-this-server.invalid"}
		}
		der, e := x509.CreateCertificate(rand.Reader, tmpl, ca, p, key)
		if e != nil {
			t.Fatal(e)
		}
		kb, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}))
		if e != nil {
			t.Fatal(e)
		}
		return cert
	}
	return testMTLS{roots: roots, client: issue(2, true), server: issue(3, false), wrongHost: issue(4, false)}
}
func tlsServer(t *testing.T, f testMTLS, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(h)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{f.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: f.roots, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
func exchange(t *testing.T, f testMTLS, url string) *HTTPSExchange {
	t.Helper()
	x, e := NewHTTPSExchange(HTTPSConfig{Enabled: true, Endpoint: url, RootCAs: f.roots, ClientCertificate: f.client, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func TestHTTPSExchangeConfigurationRefuses(t *testing.T) {
	f := mtlsFixture(t)
	for _, mode := range []string{"disabled", "http", "credentials", "query", "fragment", "roots", "cert", "key", "wrongkey", "timeout", "hugetimeout"} {
		c := HTTPSConfig{Enabled: true, Endpoint: "https://127.0.0.1/admission", RootCAs: f.roots, ClientCertificate: f.client, Timeout: time.Second}
		switch mode {
		case "disabled":
			c.Enabled = false
		case "http":
			c.Endpoint = "http://127.0.0.1"
		case "credentials":
			c.Endpoint = "https://user:secret@127.0.0.1"
		case "query":
			c.Endpoint += "?x=1"
		case "fragment":
			c.Endpoint += "#frag"
		case "roots":
			c.RootCAs = nil
		case "cert":
			c.ClientCertificate.Certificate = nil
		case "key":
			c.ClientCertificate.PrivateKey = nil
		case "wrongkey":
			_, k, _ := ed25519.GenerateKey(rand.Reader)
			c.ClientCertificate.PrivateKey = k
		case "timeout":
			c.Timeout = 0
		case "hugetimeout":
			c.Timeout = 31 * time.Second
		}
		if x, e := NewHTTPSExchange(c); x != nil || e == nil {
			t.Fatal(mode, x, e)
		}
	}
}
func TestHTTPSExchangeOnePostSignedAdmission(t *testing.T) {
	f := mtlsFixture(t)
	s := openStore(t, config(t, 1))
	it := mustPut(t, s, reading())
	trust, key := trust(t)
	var calls atomic.Int32
	server := tlsServer(t, f, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/isolated-admission" || r.Header.Get("X-HexThings-Telemetry-Topic") != it.Topic || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.ProtoMajor != 1 || !r.Close || r.Header.Get("Accept-Encoding") != "" {
			t.Error("request contract", r.Method, r.URL, r.Proto, r.Close)
		}
		b, e := io.ReadAll(r.Body)
		if e != nil || !bytes.Equal(b, it.Payload) {
			t.Error("stable input changed", e)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(token(t, receipt(it), trust, key))
	})
	// Invalid inherited proxies must not receive the request or prevent directTLS.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	x := exchange(t, f, server.URL+"/isolated-admission")
	p, e := NewPublisher(PublisherConfig{Enabled: true, Store: s, Trust: trust, Exchange: x, BatchLimit: 1})
	if e != nil {
		t.Fatal(e)
	}
	r, e := p.RunOnce(t.Context())
	if e != nil || r.Attempted != 1 || r.Reconciled != 1 || calls.Load() != 1 {
		t.Fatal(r, e, calls.Load())
	}
}
func TestHTTPSExchangeRefusalsRetainWithoutRetry(t *testing.T) {
	for _, mode := range []string{"redirect", "status", "empty", "oversize", "gzip", "wrong-media", "unsigned", "truncated", "lost-response", "cancel", "wrongca", "missing-client-cert", "hostname"} {
		t.Run(mode, func(t *testing.T) {
			f := mtlsFixture(t)
			store := openStore(t, config(t, 1))
			it := mustPut(t, store, reading())
			c, k := trust(t)
			var calls, redirected atomic.Int32
			target := tlsServer(t, f, func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(500) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			serverFixture := f
			if mode == "hostname" {
				serverFixture.server = f.wrongHost
			}
			server := tlsServer(t, serverFixture, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "redirect":
					w.Header().Set("Location", target.URL)
					w.WriteHeader(307)
					return
				case "status":
					w.WriteHeader(503)
					return
				case "empty":
					return
				case "oversize":
					w.Write(bytes.Repeat([]byte("x"), 8193))
					return
				case "gzip":
					w.Header().Set("Content-Encoding", "gzip")
				case "wrong-media":
					w.Header().Set("Content-Type", "text/plain")
				case "unsigned":
					w.Write(it.Payload)
					return
				case "truncated":
					w.Header().Set("Content-Length", "8000")
					w.Write([]byte("short"))
					return
				case "lost-response":
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				case "cancel":
					cancel()
					select {
					case <-r.Context().Done():
					case <-time.After(2 * time.Second):
					}
					return
				}
				w.Write(token(t, receipt(it), c, k))
			})
			x := exchange(t, f, server.URL)
			switch mode {
			case "wrongca":
				x.roots = mtlsFixture(t).roots
			case "missing-client-cert":
				x.certificate = tls.Certificate{}
			}
			p, e := NewPublisher(PublisherConfig{Enabled: true, Store: store, Trust: c, Exchange: x, BatchLimit: 1})
			if e != nil {
				t.Fatal(e)
			}
			r, e := p.RunOnce(ctx)
			if e == nil || r.Attempted != 1 || r.Reconciled != 0 {
				t.Fatal(r, e)
			}
			rows, e := store.Next(t.Context(), 1)
			if e != nil || len(rows) != 1 {
				t.Fatal(rows, e)
			}
			want := int32(1)
			if mode == "wrongca" || mode == "missing-client-cert" || mode == "hostname" {
				want = 0
			}
			if calls.Load() != want || redirected.Load() != 0 {
				t.Fatal("retry/redirect", calls.Load(), redirected.Load())
			}
		})
	}
}
func TestHTTPSExchangeTimeoutAndPreflight(t *testing.T) {
	f := mtlsFixture(t)
	store := openStore(t, config(t, 1))
	it := mustPut(t, store, reading())
	var calls atomic.Int32
	server := tlsServer(t, f, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	x := exchange(t, f, server.URL)
	bad := it
	bad.Topic = "foreign"
	if b, e := x.AdmitOnce(t.Context(), bad); b != nil || !errors.Is(e, ErrEvidence) || calls.Load() != 0 {
		t.Fatal(len(b), e, calls.Load())
	}
	if b, e := x.AdmitOnce(nil, it); b != nil || !errors.Is(e, ErrInput) {
		t.Fatal(len(b), e)
	}
	start := time.Now()
	if b, e := x.AdmitOnce(t.Context(), it); b != nil || e == nil {
		t.Fatal(len(b), e)
	}
	if time.Since(start) > 3*time.Second || calls.Load() != 1 {
		t.Fatal("unbounded/retried", time.Since(start), calls.Load())
	}
}
