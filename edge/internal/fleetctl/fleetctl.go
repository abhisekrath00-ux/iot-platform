// Package fleetctl handles fleet release manifests on the gateway: verify
// the referenced artifact against the local artifact store (air-gapped
// bundles pre-stage artifacts by digest), then ACK or report the exact
// failure back to the control plane. Applying the artifact is a separate,
// deliberately later step - verification and reporting ship first.
package fleetctl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// Manifest is the retained release assignment published on the fleet topic.
type Manifest struct {
	CampaignID     string  `json:"campaign_id"`
	ReleaseID      string  `json:"release_id"`
	Version        string  `json:"version"`
	ArtifactSHA256 *string `json:"artifact_sha256"`
	GatewaySerial  string  `json:"gateway_serial"`
	TenantID       string  `json:"tenant_id,omitempty"`
	Signature      string  `json:"signature,omitempty"`
}

// Ack answers a manifest on the fleet/ack topic.
type Ack struct {
	CampaignID string `json:"campaign_id"`
	State      string `json:"state"` // acked|failed
	Detail     string `json:"detail"`
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ArtifactPath resolves the local artifact for a digest inside dir. The
// digest is validated before touching the filesystem, so a hostile manifest
// cannot traverse the store path.
func ArtifactPath(dir, sha string) (string, error) {
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("artifact digest %q is not a sha256 hex string", sha)
	}
	return filepath.Join(dir, sha), nil
}

// VerifyArtifact streams the artifact and compares digests.
func VerifyArtifact(path, wantSHA string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact not staged locally: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("digest mismatch: got %s, want %s", got, wantSHA)
	}
	return nil
}

// HandleManifest processes one manifest: validate, verify, produce the ACK.
// artifactDir is the gateway's local store (e.g. /var/lib/hexmon-edge/artifacts).
func HandleManifest(payload []byte, artifactDir, gatewaySerial string) Ack {
	var m Manifest
	bad := func(detail string) Ack {
		return Ack{State: "failed", Detail: detail}
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return bad("bad manifest json: " + err.Error())
	}
	if m.CampaignID == "" || m.ReleaseID == "" || m.Version == "" {
		return bad("manifest missing campaign/release/version")
	}
	ack := Ack{CampaignID: m.CampaignID}
	if TrustedKey != nil {
		if err := VerifyManifest(TrustedKey, m); err != nil {
			ack.State, ack.Detail = "failed", "refused: "+err.Error()
			return ack
		}
		if ExpectedTenant != "" && m.TenantID != ExpectedTenant {
			ack.State, ack.Detail = "failed", "refused: manifest is for another tenant"
			return ack
		}
	}
	if m.GatewaySerial != "" && m.GatewaySerial != gatewaySerial {
		ack.State = "failed"
		ack.Detail = fmt.Sprintf("manifest addressed to %s, this gateway is %s", m.GatewaySerial, gatewaySerial)
		return ack
	}
	if m.ArtifactSHA256 == nil || *m.ArtifactSHA256 == "" {
		// Config-only release: nothing to verify.
		ack.State = "acked"
		ack.Detail = fmt.Sprintf("config release %s accepted (no artifact)", m.Version)
		return ack
	}
	path, err := ArtifactPath(artifactDir, *m.ArtifactSHA256)
	if err != nil {
		ack.State = "failed"
		ack.Detail = err.Error()
		return ack
	}
	if err := VerifyArtifact(path, *m.ArtifactSHA256); err != nil {
		ack.State = "failed"
		ack.Detail = fmt.Sprintf("release %s: %v", m.Version, err)
		return ack
	}
	ack.State = "acked"
	ack.Detail = fmt.Sprintf("release %s artifact verified (%d bytes staged, apply pending)", m.Version, fileSize(path))
	return ack
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}
