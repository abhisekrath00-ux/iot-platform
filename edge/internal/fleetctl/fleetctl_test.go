package fleetctl

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestSignedManifests(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	key := ed25519.NewKeyFromSeed(seed)
	sha := "aa" + "00000000000000000000000000000000000000000000000000000000000000"[:62]
	m := Manifest{CampaignID: "camp", ReleaseID: "rel", Version: "1.2.3", ArtifactSHA256: &sha, GatewaySerial: "SER1", TenantID: "ten"}
	// fixed vector shared with server/internal/fleet/sign_test.go
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, canonicalManifest("ten", "camp", "rel", "1.2.3", &sha, "SER1")))
	if m.Signature != "1t2D5LVp1PG1nGGov50Jzr14HmKTFk6cdKOShBcDy1292yaII+4/Og0y8U9P/LAMvoE3ndslP3t6W5HKOCiJDQ==" {
		t.Fatalf("canonical form drifted from the server: %s", m.Signature)
	}
	pub := key.Public().(ed25519.PublicKey)
	if err := VerifyManifest(pub, m); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Manifest){
		"version":   func(x *Manifest) { x.Version = "9.9.9" },
		"digest":    func(x *Manifest) { d := "bb" + sha[2:]; x.ArtifactSHA256 = &d },
		"gateway":   func(x *Manifest) { x.GatewaySerial = "SER2" },
		"tenant":    func(x *Manifest) { x.TenantID = "other" },
		"unsigned":  func(x *Manifest) { x.Signature = "" },
		"malformed": func(x *Manifest) { x.Signature = "zzz" },
	} {
		c := m
		mut(&c)
		if VerifyManifest(pub, c) == nil {
			t.Errorf("%s change accepted", name)
		}
	}
	// a wrong key is refused
	other := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	if VerifyManifest(other, m) == nil {
		t.Error("wrong key accepted")
	}
	// HandleManifest end to end: with a trusted key an unsigned config-only manifest is refused
	TrustedKey, ExpectedTenant = pub, "ten"
	defer func() { TrustedKey, ExpectedTenant = nil, "" }()
	cfgOnly := Manifest{CampaignID: "camp", ReleaseID: "rel", Version: "1", GatewaySerial: "SER1", TenantID: "ten"}
	b, _ := json.Marshal(cfgOnly)
	if a := HandleManifest(b, t.TempDir(), "SER1"); a.State != "failed" || !strings.Contains(a.Detail, "not signed") {
		t.Fatalf("unsigned accepted: %+v", a)
	}
	cfgOnly.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, canonicalManifest("ten", "camp", "rel", "1", nil, "SER1")))
	b, _ = json.Marshal(cfgOnly)
	if a := HandleManifest(b, t.TempDir(), "SER1"); a.State != "acked" {
		t.Fatalf("signed refused: %+v", a)
	}
	TrustedKey, ExpectedTenant = pub, "someone-else"
	if a := HandleManifest(b, t.TempDir(), "SER1"); a.State != "failed" {
		t.Fatalf("wrong tenant accepted: %+v", a)
	}
}
