package fleet

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Release manifests are signed with Ed25519 so an edge agent that is configured with the matching public key
// refuses anything the control plane did not sign (a tampered broker message, a message replayed to another
// gateway or tenant). The private key is a 32-byte seed in FLEET_SIGNING_KEY (base64), never stored in the
// database. Without the key, manifests go out unsigned and edges that require a signature refuse them.
//
// The canonical bytes are duplicated in edge/internal/fleetctl/sign.go; both sides test the same fixed vector.

// CanonicalManifest is the exact byte string that is signed.
func CanonicalManifest(tenant, campaign, release, version string, sha *string, serial string) []byte {
	d := "-"
	if sha != nil && *sha != "" {
		d = *sha
	}
	return []byte(strings.Join([]string{"hexmon-fleet-manifest-v1", tenant, campaign, release, version, d, serial}, "\n"))
}

// SigningKeyFromEnv returns the signing key, or nil when none is configured.
func SigningKeyFromEnv() (ed25519.PrivateKey, error) {
	raw := strings.TrimSpace(os.Getenv("FLEET_SIGNING_KEY"))
	if raw == "" {
		return nil, nil
	}
	seed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("FLEET_SIGNING_KEY must be base64 of a %d-byte seed", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// Sign returns the base64 signature of the manifest.
func Sign(key ed25519.PrivateKey, tenant, campaign, release, version string, sha *string, serial string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, CanonicalManifest(tenant, campaign, release, version, sha, serial)))
}
