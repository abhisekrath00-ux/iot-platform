package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
)

func TestBuildManifestSigned(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	sha := "cc" + "00000000000000000000000000000000000000000000000000000000000000"[:62]
	raw := buildManifest(key, "ten", "camp", "rel", "2.0", &sha, "SER9")
	var f map[string]any
	json.Unmarshal(raw, &f)
	sig, _ := base64.StdEncoding.DecodeString(f["signature"].(string))
	if f["tenant_id"] != "ten" || !ed25519.Verify(key.Public().(ed25519.PublicKey), fleet.CanonicalManifest("ten", "camp", "rel", "2.0", &sha, "SER9"), sig) {
		t.Fatalf("manifest not verifiably signed: %s", raw)
	}
	var u map[string]any
	json.Unmarshal(buildManifest(nil, "ten", "camp", "rel", "2.0", nil, "SER9"), &u)
	if _, has := u["signature"]; has {
		t.Fatal("unsigned manifest carries a signature")
	}
}
