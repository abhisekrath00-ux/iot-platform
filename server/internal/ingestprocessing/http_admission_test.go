package ingestprocessing

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func httpConfig(t *testing.T, f httpMTLS, x Engine) AdmissionHTTPConfig {
	return AdmissionHTTPConfig{Enabled: true, Engine: x, Signing: signing(t), Path: "/staged/processing-v1/admit", Routes: []CertificateRoute{{LeafSHA256: sha256.Sum256(f.client.Certificate[0]), Topic: "t/a/g/g/telemetry"}}, Timeout: 3 * time.Second}
}
func TestAdmissionHTTPConfiguration(t *testing.T) {
	f := httpTLSFixture(t)
	for _, mode := range []string{"disabled", "engine", "pool", "signer", "path", "routes", "pin", "topic", "duplicate", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			c := httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
			switch mode {
			case "disabled":
				c.Enabled = false
			case "engine":
				c.Engine.Enabled = false
			case "pool":
				c.Engine.Pool = nil
			case "signer":
				c.Signing.PrivateKey = nil
			case "path":
				c.Path = "/arbitrary"
			case "routes":
				c.Routes = nil
			case "pin":
				c.Routes[0].LeafSHA256 = [32]byte{}
			case "topic":
				c.Routes[0].Topic = "t/+/g/g/telemetry"
			case "duplicate":
				c.Routes = append(c.Routes, c.Routes[0])
			case "timeout":
				c.Timeout = time.Minute
			}
			if h, e := NewAdmissionHTTP(c); h != nil || e == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	c := httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
	h, e := NewAdmissionHTTP(c)
	if e != nil {
		t.Fatal(e)
	}
	c.Signing.PrivateKey[0] ^= 1
	c.Routes[0].Topic = "t/b/g/g/telemetry"
	if _, e = receiptSigningKey(h.signing); e != nil || h.routes[sha256.Sum256(f.client.Certificate[0])] != "t/a/g/g/telemetry" {
		t.Fatal("caller mutated frozen config", e)
	}
}
func TestAdmissionHTTPRefusesBeforeDatabase(t *testing.T) {
	f := httpTLSFixture(t)
	h, e := NewAdmissionHTTP(httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"plaintext", "unverified", "foreign-leaf", "foreign-topic", "duplicate-topic", "method", "path", "query", "encodedpath", "type", "encoding", "oversize", "malformed", "payload-route", "actual-foreign", "absent-client", "proxy-spoof", "duplicate-type", "stream-oversize", "chain-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			s := httpTLSServer(t, f, h.ServeHTTP)
			body := `{"event_id":"x","device_id":"d","point_id":"p","observed_at":"2026-01-01T00:00:00Z","quality":"measured","schema_version":1,"value":1}`
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+"/staged/processing-v1/admit", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
			switch mode {
			case "plaintext", "unverified", "foreign-leaf", "proxy-spoof", "chain-mismatch":
				r := httptest.NewRequest(http.MethodPost, "https://localhost/staged/processing-v1/admit", strings.NewReader(body))
				r.Header = req.Header.Clone()
				if mode == "plaintext" || mode == "proxy-spoof" {
					r.Header.Set("X-SSL-Client-Verify", "SUCCESS")
					r.Header.Set("X-Forwarded-Client-Cert", "synthetic")
					r.TLS = nil
				} else if mode == "unverified" {
					r.TLS = &tls.ConnectionState{HandshakeComplete: true}
				} else if mode == "chain-mismatch" {
					r.TLS = &tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{mustCert(t, f.client.Certificate[0])}, VerifiedChains: [][]*x509.Certificate{{mustCert(t, f.server.Certificate[0])}}}
				} else {
					r.TLS = &tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{mustCert(t, f.server.Certificate[0])}, VerifiedChains: [][]*x509.Certificate{{mustCert(t, f.server.Certificate[0])}}}
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 403 {
					t.Fatal(w.Code)
				}
				return
			case "duplicate-type":
				req.Header.Add("Content-Type", "application/json")
			case "stream-oversize":
				req.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 8193)))
				req.ContentLength = -1
			case "foreign-topic":
				req.Header.Set("X-HexThings-Telemetry-Topic", "t/b/g/g/telemetry")
			case "duplicate-topic":
				req.Header.Add("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
			case "method":
				req.Method = http.MethodGet
			case "path":
				req.URL.Path = "/other"
			case "query":
				req.URL.RawQuery = "x=1"
			case "encodedpath":
				req.URL.RawPath = "/staged/processing-v1/%61dmit"
			case "type":
				req.Header.Set("Content-Type", "text/plain")
			case "encoding":
				req.Header.Set("Content-Encoding", "gzip")
			case "oversize":
				req.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 8193)))
				req.ContentLength = 8193
			case "malformed":
				req.Body = io.NopCloser(strings.NewReader("{"))
				req.ContentLength = 1
			case "payload-route":
				req.Body = io.NopCloser(strings.NewReader(strings.Replace(body, `"event_id":"x"`, `"event_id":"x","tenant_id":"b"`, 1)))
				req.ContentLength = -1
			}
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}, Timeout: 5 * time.Second}
			if mode == "actual-foreign" {
				client.Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{f.foreign}
			}
			if mode == "absent-client" {
				client.Transport.(*http.Transport).TLSClientConfig.Certificates = nil
			}
			response, err := client.Do(req)
			if mode == "absent-client" {
				if err == nil {
					response.Body.Close()
					t.Fatal("missing client accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode < 400 || response.StatusCode >= 500 {
				t.Fatal(response.StatusCode)
			}
		})
	}
}

// lostAdmissionResponse drops the first response only AFTER the real handler
// invokes Write, meaning actual admission/signing completed. No bytes are forged.
type lostAdmissionResponse struct {
	http.ResponseWriter
	drop bool
}

func (w lostAdmissionResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w lostAdmissionResponse) WriteHeader(code int) {
	if !w.drop {
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w lostAdmissionResponse) Write(b []byte) (int, error) {
	if !w.drop {
		return w.ResponseWriter.Write(b)
	}
	c, _, e := http.NewResponseController(w.ResponseWriter).Hijack()
	if e != nil {
		return 0, e
	}
	c.Close()
	return 0, errors.New("synthetic response lost after real commit")
}
func TestAdmissionHTTPLivePGAndEdge(t *testing.T) {
	x, envelope := fixture(t)
	f := httpTLSFixture(t)
	c := httpConfig(t, f, x)
	h, e := NewAdmissionHTTP(c)
	if e != nil {
		t.Fatal(e)
	}
	var requests atomic.Int32
	s := httpTLSServer(t, f, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		h.ServeHTTP(lostAdmissionResponse{w, n == 1}, r)
	})
	kb, e := x509.MarshalPKCS8PrivateKey(f.client.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.client.Certificate[0]})
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}, Timeout: 5 * time.Second}
	data, e := json.Marshal(struct {
		Endpoint                           string
		Roots, Certificate, Key, PublicKey []byte
		Audience                           string
		Reading                            Envelope
	}{s.URL + c.Path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.ca}), cert, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), c.Signing.PrivateKey.Public().(ed25519.PublicKey), c.Signing.Audience, envelope})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "synthetic-live-handler.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "go", "test", "./internal/admissionoutbox", "-run", "^TestLiveAdmissionHTTPFixture$", "-count=1", "-v")
	cmd.Dir = filepath.Join("..", "..", "..", "edge")
	cmd.Env = append(os.Environ(), "TEST_LIVE_ADMISSION_HTTP="+path)
	out, e := cmd.CombinedOutput()
	if e != nil || !bytes.Contains(out, []byte("LIVE_HANDLER_PG_MTLS_EDGE_REPLAY_OK")) {
		t.Fatalf("live handler bridge: %v\n%s", e, out)
	}
	if requests.Load() != 2 {
		t.Fatal("unexpected automatic request", requests.Load())
	}
	counts(t, x.Pool, envelope.EventID, 1)
	historyCount(t, x, 0, "processing_started")
	// A conflicting same identity is refused with NO signed receipt.
	changed := envelope
	changed.Value++
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+c.Path, bytes.NewReader(wirePayload(t, changed)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
	response, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || bytes.Contains(body, []byte("signature")) {
		t.Fatal(response.StatusCode, string(body))
	}
	counts(t, x.Pool, envelope.EventID, 1)
	exec(t, x.Pool, `CREATE FUNCTION http_refusal() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic refusal'; END $$ LANGUAGE plpgsql; CREATE TRIGGER http_refusal BEFORE INSERT ON telemetry FOR EACH ROW EXECUTE FUNCTION http_refusal()`)
	fresh := envelope
	fresh.EventID = "http-refused"
	req, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+c.Path, bytes.NewReader(wirePayload(t, fresh)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
	response, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || bytes.Contains(body, []byte("signature")) {
		t.Fatal(response.StatusCode, string(body))
	}
	counts(t, x.Pool, fresh.EventID, 0)

	t.Log("LIVE_HANDLER_PG_MTLS_EDGE_REPLAY_OK")
}

func mustCert(t *testing.T, b []byte) *x509.Certificate {
	t.Helper()
	c, e := x509.ParseCertificate(b)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestAdmissionHTTPBodyDeadlineAndUnsupportedWriter(t *testing.T) {
	f := httpTLSFixture(t)
	c := httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
	c.Timeout = time.Second
	h, e := NewAdmissionHTTP(c)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest(http.MethodPost, "https://localhost"+c.Path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
	leaf := mustCert(t, f.client.Certificate[0])
	req.TLS = &tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatal("unsupported deadlines accepted", w.Code)
	}
	server := httpTLSServer(t, f, h.ServeHTTP)
	conn, e := tls.Dial("tcp", strings.TrimPrefix(server.URL, "https://"), &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}})
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	start := time.Now()
	_, e = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-HexThings-Telemetry-Topic: t/a/g/g/telemetry\r\nContent-Length: 20\r\n\r\n{", c.Path)
	if e != nil {
		t.Fatal(e)
	}
	response, e := http.ReadResponse(bufio.NewReader(conn), nil)
	if e == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("slow incomplete body accepted")
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatal("body read was not bounded", elapsed, e)
	}
}
