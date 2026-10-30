package redisx

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/respcache"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestValkeyTransport uses a real optional vendor binary, never the RESP fake.
func TestValkeyTransport(t *testing.T) {
	binary := os.Getenv("TEST_VALKEY_BINARY")
	if binary == "" {
		t.Skip("TEST_VALKEY_BINARY not supplied")
	}
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"localhost"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	write := func(name, kind string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: b}), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	caFile := write("ca.pem", "CERTIFICATE", caDER)
	certFile := write("cert.pem", "CERTIFICATE", der)
	keyFile := write("key.pem", "EC PRIVATE KEY", kb)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	conf := filepath.Join(dir, "valkey.conf")
	// No plaintext listener, mTLS required, default user disabled, app ACL restricted.
	config := fmt.Sprintf("bind 127.0.0.1\nport 0\ntls-port %d\ntls-cert-file %s\ntls-key-file %s\ntls-ca-cert-file %s\ntls-auth-clients yes\nsave \"\"\nappendonly no\nuser default off\nuser app on >local-test-only ~hx:* +ping +get +set +getdel +incr +select\n", port, certFile, keyFile, caFile)
	if err := os.WriteFile(conf, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(binary, conf)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	raw := fmt.Sprintf("rediss://app:local-test-only@localhost:%d/2", port)
	opt := TLSOptions{CAFile: caFile, CertFile: certFile, KeyFile: keyFile}
	c, err := FromURL(raw, opt)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s, e := c.Cmd("PING"); e == nil && s == "PONG" {
			break
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(filepath.Join(dir, "log"))
			t.Fatalf("Valkey start failed: %s", b)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := c.Set("hx:k", "value", time.Second); err != nil {
		t.Fatal(err)
	}
	if v, ok, e := c.Get("hx:k"); e != nil || !ok || v != "value" {
		t.Fatal("GET/SET", v, ok, e)
	}
	if v, e := c.Cmd("SET", "hx:nonce", "nonce", "EX", "10", "NX"); e != nil || v != "OK" {
		t.Fatal("NX", v, e)
	}
	if v, e := c.Cmd("SET", "hx:nonce", "replacement", "EX", "10", "NX"); e != nil || v != "" {
		t.Fatal("NX overwrite", v, e)
	}
	if v, e := c.Cmd("GETDEL", "hx:nonce"); e != nil || v != "nonce" {
		t.Fatal("GETDEL", v, e)
	}
	if v, e := c.Cmd("GETDEL", "hx:nonce"); e != nil || v != "" {
		t.Fatal("replay", v, e)
	}
	if n, e := c.Incr("hx:gen"); e != nil || n != 1 {
		t.Fatal("INCR", n, e)
	}
	c0, _ := FromURL(strings.TrimSuffix(raw, "/2")+"/0", opt)
	if _, ok, e := c0.Get("hx:k"); e != nil || ok {
		t.Fatal("database isolation")
	}
	if _, err := c.Cmd("SET", "outside", "no"); err == nil {
		t.Fatal("ACL key boundary")
	}
	if _, err := c.Cmd("FLUSHALL"); err == nil {
		t.Fatal("ACL command boundary")
	}
	bad, _ := FromURL(strings.Replace(raw, "local-test-only", "wrong", 1), opt)
	if _, err := bad.Cmd("PING"); err == nil {
		t.Fatal("wrong password accepted")
	}
	untrusted, _ := FromURL(raw, TLSOptions{CertFile: certFile, KeyFile: keyFile})
	if _, err := untrusted.Cmd("PING"); err == nil {
		t.Fatal("untrusted CA accepted")
	}
	wrongHost, _ := FromURL(strings.Replace(raw, "localhost", "127.0.0.1", 1), opt)
	if _, err := wrongHost.Cmd("PING"); err == nil {
		t.Fatal("wrong certificate hostname accepted")
	}
	noCert, _ := FromURL(raw, TLSOptions{CAFile: caFile})
	if _, err := noCert.Cmd("PING"); err == nil {
		t.Fatal("missing mTLS cert accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CmdContext(ctx, "PING"); err == nil {
		t.Fatal("cancelled command ran")
	}
	// Two separate response-cache instances use the same real TLS endpoint.
	c2, _ := FromURL(raw, opt)
	cache1, cache2 := respcache.New(4), respcache.New(4)
	cache1.UseShared(c)
	cache2.UseShared(c2)
	value := "old"
	calls := 0
	handler := func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, value) }
	read := func(cache *respcache.Cache) string {
		w := httptest.NewRecorder()
		cache.Middleware(time.Second, func(*http.Request) string { return "test-tenant" }, handler)(w, httptest.NewRequest("GET", "/values", nil))
		return w.Body.String()
	}
	if read(cache1) != "old" || read(cache2) != "old" || calls != 1 {
		t.Fatal("cross-replica shared hit", calls)
	}
	value = "new"
	cache1.Invalidate("test-tenant")
	if read(cache2) != "new" || calls != 2 {
		t.Fatal("cross-replica invalidation", calls)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, ok, e := c.Get("hx:k"); e != nil || ok {
		t.Fatal("expiry")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if _, err := c.Cmd("PING"); err == nil {
		t.Fatal("outage did not fail")
	}
	value = "after-outage"
	if read(cache2) != "after-outage" || calls != 3 {
		t.Fatal("outage did not bypass cache", calls)
	}
	value = "after-second-write"
	if read(cache2) != "after-second-write" || calls != 4 {
		t.Fatal("outage created a local stale cache", calls)
	}
}
