package fleet

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

// The same fixed vector is asserted in edge/internal/fleetctl so the two copies of the canonical form cannot drift.
const vectorSig = "1t2D5LVp1PG1nGGov50Jzr14HmKTFk6cdKOShBcDy1292yaII+4/Og0y8U9P/LAMvoE3ndslP3t6W5HKOCiJDQ=="

func TestSignVector(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	key := ed25519.NewKeyFromSeed(seed)
	sha := "aa" + "00000000000000000000000000000000000000000000000000000000000000"[:62]
	got := Sign(key, "ten", "camp", "rel", "1.2.3", &sha, "SER1")
	t.Logf("signature vector: %s", got)
	if got != vectorSig {
		t.Fatalf("signature changed: %s", got)
	}
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), CanonicalManifest("ten", "camp", "rel", "1.2.3", &sha, "SER1"), mustDecode(t, got)) {
		t.Fatal("does not verify")
	}
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64Std.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var base64Std = base64.StdEncoding
