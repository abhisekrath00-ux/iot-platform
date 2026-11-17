package ingestprocessing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type httpMTLS struct {
	ca        []byte
	roots     *x509.CertPool
	client    tls.Certificate
	foreign   tls.Certificate
	server    tls.Certificate
	wrongHost tls.Certificate
}

func httpTLSFixture(t *testing.T) httpMTLS {
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
	return httpMTLS{ca: caDER, roots: roots, client: issue(2, true), foreign: issue(5, true), server: issue(3, false), wrongHost: issue(4, false)}
}
func httpTLSServer(t *testing.T, f httpMTLS, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(h)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{f.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: f.roots, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
