package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealOpenRoundTripAndBinding(t *testing.T) {
	blob, err := Seal(key(1), "t1", "api", []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("hunter2")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	got, err := Open(key(1), "t1", "api", blob)
	if err != nil || string(got) != "hunter2" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := Open(key(1), "t2", "api", blob); err == nil {
		t.Fatal("opened under another tenant")
	}
	if _, err := Open(key(1), "t1", "other", blob); err == nil {
		t.Fatal("opened under another name")
	}
	if _, err := Open(key(2), "t1", "api", blob); err == nil {
		t.Fatal("opened with the wrong key")
	}
	blob[len(blob)-1] ^= 1
	if _, err := Open(key(1), "t1", "api", blob); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	b2, _ := Seal(key(1), "t1", "api", []byte("hunter2"))
	b3, _ := Seal(key(1), "t1", "api", []byte("hunter2"))
	if bytes.Equal(b2, b3) {
		t.Fatal("nonce reused")
	}
}

func TestKeyFromEnv(t *testing.T) {
	if k, err := KeyFromEnv(""); k != nil || err != nil {
		t.Fatal("empty should mean not configured")
	}
	if _, err := KeyFromEnv("not base64!"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := KeyFromEnv(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short key accepted")
	}
	if k, err := KeyFromEnv(base64.StdEncoding.EncodeToString(key(3))); err != nil || len(k) != 32 {
		t.Fatal("valid key rejected")
	}
	if _, err := Seal(nil, "t", "n", []byte("x")); err != ErrNoKey {
		t.Fatalf("seal without key = %v", err)
	}
}
