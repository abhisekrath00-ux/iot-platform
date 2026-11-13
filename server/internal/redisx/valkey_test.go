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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	config := fmt.Sprintf("bind 127.0.0.1\nport 0\ntls-port %d\ntls-cert-file %s\ntls-key-file %s\ntls-ca-cert-file %s\ntls-auth-clients yes\nsave \"\"\nappendonly no\nuser default off\nuser reader on >local-test-only ~hx:* +ping +get +select\nuser noinc on >local-test-only ~hx:* +ping +get +set +select\nuser app on >local-test-only ~hx:* +ping +get +set +getdel +incr +select +ttl\n", port, certFile, keyFile, caFile)
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
	if err := checkCachePermissions(context.Background(), c.CmdContext, "expiry-proof"); err != nil {
		t.Fatal("actual mTLS ACL preflight", err)
	}
	for _, user := range []string{"reader", "noinc"} {
		restricted, _ := FromURL(strings.Replace(raw, "app:", user+":", 1), opt)
		if err := restricted.CheckCachePermissions(context.Background()); err == nil {
			t.Fatal("restricted ACL preflight passed", user)
		}
	}

	for _, key := range []string{"hx:rc:preflight:expiry-proof", "hx:gen:preflight:expiry-proof"} {
		ttl, e := c.Cmd("TTL", key)
		n, parseErr := strconv.Atoi(ttl)
		if e != nil || parseErr != nil || n < 1 || n > 30 {
			t.Fatal("scratch TTL not preserved", ttl, e)
		}
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
	// Bounded concurrent reads across two in-process caches backed by the actual
	// TLS service. Each invalidation completes before the next generation begins;
	// this is not a claim of linearizable reads during an overlapping write.
	var generation atomic.Int32
	generation.Store(1)
	var backendCalls atomic.Int32
	mixedHandler := func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		fmt.Fprint(w, generation.Load())
	}
	for want := int32(1); want <= 5; want++ {
		generation.Store(want)
		cache1.Invalidate("parallel-test")
		var wg sync.WaitGroup
		errors := make(chan string, 12)
		for worker := 0; worker < 12; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				cache := []*respcache.Cache{cache1, cache2}[worker%2]
				w := httptest.NewRecorder()
				cache.Middleware(time.Second, func(*http.Request) string { return "parallel-test" }, mixedHandler)(w, httptest.NewRequest("GET", "/parallel", nil))
				if w.Body.String() != fmt.Sprint(want) {
					errors <- w.Body.String()
				}
			}(worker)
		}
		wg.Wait()
		close(errors)
		for got := range errors {
			t.Errorf("completed shared invalidation served old generation: got=%s want=%d", got, want)
		}
	}
	if backendCalls.Load() < 5 {
		t.Fatal("generations did not reach the actual backend")
	}
	// Exercise failed-flight cleanup through an actual shared transport, not
	// only the deterministic unit fixture. Partial output never reaches Valkey.
	var panicCalls int
	panicHandler := func(w http.ResponseWriter, r *http.Request) {
		panicCalls++
		if panicCalls == 1 {
			fmt.Fprint(w, "partial")
			panic("synthetic shared handler failure")
		}
		fmt.Fprint(w, "fresh-after-panic")
	}
	panicRead := func(cache *respcache.Cache) string {
		w := httptest.NewRecorder()
		cache.Middleware(time.Second, func(*http.Request) string { return "panic-test" }, panicHandler)(w, httptest.NewRequest("GET", "/panic", nil))
		return w.Body.String()
	}
	func() {
		defer func() {
			if recover() != "synthetic shared handler failure" {
				t.Fatal("actual shared-cache handler panic was swallowed or changed")
			}
		}()
		panicRead(cache1)
	}()
	if panicRead(cache1) != "fresh-after-panic" || panicRead(cache2) != "fresh-after-panic" || panicCalls != 2 {
		t.Fatal("actual shared-cache panic poisoned flight or stored partial response")
	}
	t.Logf("actual mTLS Valkey: two cache instances,5generations x12concurrent reads after completed invalidations, failed-flight recovery, expiry and outage bypass; one local endpoint only")
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
