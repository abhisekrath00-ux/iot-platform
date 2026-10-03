package fleetctl

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// Signed manifests. When TrustedKey is set (config fleet_public_key), HandleManifest refuses any manifest
// whose Ed25519 signature does not verify, and any manifest for another tenant. The canonical bytes must stay
// identical to server/internal/fleet/sign.go; both sides test the same fixed vector.
var (
	TrustedKey     ed25519.PublicKey
	ExpectedTenant string
)

// ParsePublicKey decodes the base64 32-byte public key from the edge config.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("fleet_public_key must be base64 of a %d-byte Ed25519 public key", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

func canonicalManifest(tenant, campaign, release, version string, sha *string, serial string) []byte {
	d := "-"
	if sha != nil && *sha != "" {
		d = *sha
	}
	return []byte(strings.Join([]string{"hexmon-fleet-manifest-v1", tenant, campaign, release, version, d, serial}, "\n"))
}

// VerifyManifest checks the signature against the trusted key.
func VerifyManifest(key ed25519.PublicKey, m Manifest) error {
	if m.Signature == "" {
		return fmt.Errorf("manifest is not signed")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("manifest signature is malformed")
	}
	if !ed25519.Verify(key, canonicalManifest(m.TenantID, m.CampaignID, m.ReleaseID, m.Version, m.ArtifactSHA256, m.GatewaySerial), sig) {
		return fmt.Errorf("manifest signature does not verify")
	}
	return nil
}
