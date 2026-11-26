//go:build linux

package setup

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func commissioningFixture(t *testing.T) []byte {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, c, c, &k.PublicKey, k)
	if e != nil {
		t.Fatal(e)
	}
	p := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _ := json.Marshal(commissioningInput{"https://example.invalid", "serial", "synthetic-code", p, p, "tenant", key})
	return b
}
func TestCommissioningStrictInputAndPrivateFiles(t *testing.T) {
	raw := commissioningFixture(t)
	if _, e := parseCommissioning(raw); e != nil {
		t.Fatal(e)
	}
	for _, extra := range []string{`,"Dir":"/tmp/injected"}`, `,"Targets":{}}`, `,"operation":"create"}`, `,"Server":"https://other.invalid"}`, `,"server":"https://other.invalid"}`} {
		if _, e := parseCommissioning(append(append([]byte{}, raw[:len(raw)-1]...), []byte(extra)...)); e == nil {
			t.Fatal("ambiguous shape accepted")
		}
	}
	if _, e := parseCommissioning(append(raw, []byte(` {}`)...)); e == nil {
		t.Fatal("trailing input")
	}
	d := t.TempDir()
	os.Chmod(d, 0700)
	p := filepath.Join(d, "input")
	if e := os.WriteFile(p, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := privateCommissioningFile(p); e != nil {
		t.Fatal(e)
	}
	os.Chmod(p, 0644)
	if _, e := privateCommissioningFile(p); e == nil {
		t.Fatal("public file accepted")
	}
	os.Chmod(p, 0700)
	if _, e := privateCommissioningFile(p); e == nil {
		t.Fatal("executable input accepted")
	}
	os.Chmod(p, 0600)
	os.Symlink(p, filepath.Join(d, "link"))
	if _, e := privateCommissioningFile(filepath.Join(d, "link")); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Link(p, filepath.Join(d, "hard"))
	if _, e := privateCommissioningFile(p); e == nil {
		t.Fatal("hardlink accepted")
	}
	os.Remove(filepath.Join(d, "hard"))
	os.WriteFile(p, bytes.Repeat([]byte("x"), 131073), 0600)
	if _, e := privateCommissioningFile(p); e == nil {
		t.Fatal("oversize accepted")
	}
	fifo := filepath.Join(d, "fifo")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := privateCommissioningFile(fifo); e == nil {
		t.Fatal("fifo accepted")
	}
}
func TestCommissioningCancelAndWrongIndependentAnchor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root refusal separately exercised")
	}
	raw := commissioningFixture(t)
	d := t.TempDir()
	os.Chmod(d, 0700)
	p := filepath.Join(d, "input")
	os.WriteFile(p, raw, 0600)
	var o bytes.Buffer
	e := RunCommissioningImport(strings.NewReader(p+"\nwrong\n"), &o)
	if e == nil || !strings.Contains(e.Error(), "independent") {
		t.Fatal(e)
	}
	if strings.Contains(o.String(), "synthetic-code") {
		t.Fatal("secret echoed")
	}
	o.Reset()
	e = RunCommissioningImport(strings.NewReader("\n"), &o)
	if e == nil {
		t.Fatal("cancel accepted")
	}
}

func TestCommissioningAncestorAndPinnedBytes(t *testing.T) {
	d := t.TempDir()
	os.Chmod(d, 0700)
	real := filepath.Join(d, "real")
	os.Mkdir(real, 0700)
	p := filepath.Join(real, "input")
	raw := commissioningFixture(t)
	os.WriteFile(p, raw, 0600)
	link := filepath.Join(d, "alias")
	os.Symlink(real, link)
	if _, e := privateCommissioningFile(filepath.Join(link, "input")); e == nil {
		t.Fatal("symlink ancestor accepted")
	}
	snapshot, e := privateCommissioningFile(p)
	if e != nil {
		t.Fatal(e)
	}
	os.Rename(p, p+".old")
	os.WriteFile(p, []byte("changed"), 0600)
	if !bytes.Equal(snapshot, raw) {
		t.Fatal("reviewed bytes followed replacement")
	}
	f, e := commissionDirectory(real)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	os.Rename(real, real+".old")
	os.Mkdir(real, 0700)
	g, e := commissionDirectory(real)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	a, _ := f.Stat()
	b, _ := g.Stat()
	if os.SameFile(a, b) {
		t.Fatal("replaced parent not detected")
	}
}

type commissionScriptReader struct {
	lines  []string
	at     int
	before func(int)
}

func (r *commissionScriptReader) Read(p []byte) (int, error) {
	if r.at == len(r.lines) {
		return 0, io.EOF
	}
	if r.before != nil {
		r.before(r.at)
	}
	s := r.lines[r.at]
	r.at++
	if len(s) > len(p) {
		panic("test line too long")
	}
	return copy(p, s), nil
}
func TestCommissioningManagerParentSwapBeforePreparation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root adapter")
	}
	d := t.TempDir()
	os.Chmod(d, 0700)
	inputDir := filepath.Join(d, "in")
	attemptParent := filepath.Join(d, "attempts")
	preparedParent := filepath.Join(d, "payloads")
	accept := filepath.Join(d, "accepted")
	for _, p := range []string{inputDir, attemptParent, preparedParent, accept} {
		os.Mkdir(p, 0700)
	}
	raw := commissioningFixture(t)
	file := filepath.Join(inputDir, "input")
	os.WriteFile(file, raw, 0600)
	v, _ := parseCommissioning(raw)
	r := &commissionScriptReader{lines: []string{file + "\n", commissionHash([]byte(v.TransportCA)) + "\n", commissionHash([]byte(v.DeviceCA)) + "\n", commissionHash(v.ResponseKey) + "\n", filepath.Join(attemptParent, "new") + "\n", "CREATE BOUND\n", filepath.Join(preparedParent, "new") + "\n", accept + "\n", "identity\n", "PREPARE NEW PRIVATE PAYLOAD\n"}}
	r.before = func(i int) {
		if i == 9 {
			os.Rename(attemptParent, attemptParent+".old")
			os.Mkdir(attemptParent, 0700)
		}
	}
	var out bytes.Buffer
	e := RunCommissioningImport(r, &out)
	if e == nil || !strings.Contains(e.Error(), "reviewed path changed") {
		t.Fatal("path swap not refused", e)
	}
	if _, e := os.Stat(filepath.Join(preparedParent, "new")); !os.IsNotExist(e) {
		t.Fatal("path swap reached preparation")
	}
}
