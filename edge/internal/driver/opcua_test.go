package driver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestOPCUAOptionsRefuseAmbiguousSecurity(t *testing.T) {
	bad := []config.Device{
		{ID: "a", Security: "sign-and-encrypt"},
		{ID: "b", Security: "weird"},
		{ID: "c", Username: "u"},
	}
	for _, d := range bad {
		if _, err := opcuaOptions(d); err == nil {
			t.Errorf("%s should be refused", d.ID)
		}
	}
	t.Setenv("OPC_PW", "x")
	if _, err := opcuaOptions(config.Device{ID: "ok", Username: "u", PasswordEnv: "OPC_PW"}); err != nil {
		t.Fatal(err)
	}
	// Secure modes need a pinned, valid server certificate.
	if _, err := opcuaOptions(config.Device{ID: "nopin", Security: "sign", ClientCert: "c.pem", ClientKey: "k.pem"}); err == nil {
		t.Fatal("secure mode without server_cert must be refused")
	}
	dir := t.TempDir()
	good := writeCert(t, dir, "good", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	if _, err := opcuaOptions(config.Device{ID: "ok2", Security: "sign", ClientCert: "c.pem", ClientKey: "k.pem", ServerCert: good}); err != nil {
		t.Fatal(err)
	}
	der := writeCert(t, dir, "der", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), false)
	if _, err := opcuaOptions(config.Device{ID: "ok3", Security: "sign-and-encrypt", ClientCert: "c.pem", ClientKey: "k.pem", ServerCert: der}); err != nil {
		t.Fatalf("DER cert refused: %v", err)
	}
	expired := writeCert(t, dir, "old", time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour), true)
	future := writeCert(t, dir, "new", time.Now().Add(time.Hour), time.Now().Add(2*time.Hour), true)
	junk := filepath.Join(dir, "junk.pem")
	os.WriteFile(junk, []byte("not a cert"), 0o600)
	for name, p := range map[string]string{"expired": expired, "not yet valid": future, "junk": junk, "missing": filepath.Join(dir, "none.pem")} {
		if _, err := opcuaOptions(config.Device{ID: "bad", Security: "sign", ClientCert: "c.pem", ClientKey: "k.pem", ServerCert: p}); err == nil {
			t.Errorf("%s server_cert accepted", name)
		}
	}
}

func writeCert(t *testing.T, dir, name string, from, to time.Time, asPEM bool) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: from, NotAfter: to}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	out := der
	if asPEM {
		out = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
