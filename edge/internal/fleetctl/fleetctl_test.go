package fleetctl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func stage(t *testing.T, content []byte) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	sum := sha256.Sum256(content)
	sha = hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dir, sha), content, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, sha
}

func manifest(campaign, serial, sha string) string {
	if sha == "" {
		return fmt.Sprintf(`{"campaign_id":%q,"release_id":"rel-1","version":"1.2.0","gateway_serial":%q,"artifact_sha256":null}`, campaign, serial)
	}
	return fmt.Sprintf(`{"campaign_id":%q,"release_id":"rel-1","version":"1.2.0","gateway_serial":%q,"artifact_sha256":%q}`, campaign, serial, sha)
}

func TestVerifyHappyPath(t *testing.T) {
	dir, sha := stage(t, []byte("firmware-bytes"))
	ack := HandleManifest([]byte(manifest("camp-1", "AXON-0001", sha)), dir, "AXON-0001")
	if ack.State != "acked" || ack.CampaignID != "camp-1" {
		t.Fatalf("ack: %+v", ack)
	}
}

func TestDigestMismatchFails(t *testing.T) {
	dir, _ := stage(t, []byte("real"))
	other := sha256.Sum256([]byte("other"))
	ack := HandleManifest([]byte(manifest("camp-1", "AXON-0001", hex.EncodeToString(other[:]))), dir, "AXON-0001")
	if ack.State != "failed" {
		t.Fatalf("mismatch accepted: %+v", ack)
	}
}

func TestMissingArtifactFails(t *testing.T) {
	dir, sha := stage(t, []byte("x"))
	os.Remove(filepath.Join(dir, sha))
	ack := HandleManifest([]byte(manifest("camp-1", "AXON-0001", sha)), dir, "AXON-0001")
	if ack.State != "failed" {
		t.Fatalf("missing artifact accepted: %+v", ack)
	}
}

func TestTraversalRejected(t *testing.T) {
	if _, err := ArtifactPath("/tmp/store", "../../etc/passwd"); err == nil {
		t.Fatal("traversal digest accepted")
	}
	ack := HandleManifest([]byte(manifest("camp-1", "AXON-0001", "../../../../etc/passwd")), "/tmp", "AXON-0001")
	if ack.State != "failed" {
		t.Fatalf("traversal manifest accepted: %+v", ack)
	}
}

func TestWrongAddresseeFails(t *testing.T) {
	dir, sha := stage(t, []byte("x"))
	ack := HandleManifest([]byte(manifest("camp-1", "AXON-9999", sha)), dir, "AXON-0001")
	if ack.State != "failed" {
		t.Fatalf("wrong-serial manifest accepted: %+v", ack)
	}
}

func TestConfigOnlyReleaseAcks(t *testing.T) {
	ack := HandleManifest([]byte(manifest("camp-1", "AXON-0001", "")), "/nonexistent", "AXON-0001")
	if ack.State != "acked" {
		t.Fatalf("config-only release failed: %+v", ack)
	}
}

func TestBadJSONFails(t *testing.T) {
	ack := HandleManifest([]byte("{nope"), "/tmp", "AXON-0001")
	if ack.State != "failed" {
		t.Fatalf("bad json accepted: %+v", ack)
	}
}
