package claim

// Experimental protocol client. Product commands explicitly select new-attempt
// creation or same-attempt recovery; neither installs identity or starts service.
import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type candidateIntent struct {
	ID, Server, Serial, CSR          string
	Tenant, ResponsePin, DeviceRoots string
}

func candidateTrustDigest(pin ed25519.PublicKey, deviceTrustDER []byte) (string, string) {
	h := sha256.Sum256(pin)
	rp := hex.EncodeToString(h[:])
	h = sha256.Sum256(deviceTrustDER)
	return rp, hex.EncodeToString(h[:])
}

func candidateProof(v candidateIntent, recoverOnly bool) []byte {
	op := "claim"
	if recoverOnly {
		op = "recover"
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("hexthings-claim-v1\x00%s\x00%s\x00%s\x00%s", op, v.ID, v.Serial, v.CSR)))
	return h[:]
}

// CandidateClaim creates or recovers an explicit attempt. Recovery is a distinct
// read-result endpoint, never a repeat of claim. Device roots are supplied
// separately from the server response and HTTPS transport trust.
func CandidateClaim(ctx context.Context, client *http.Client, dir, server, code, serial string, recoverOnly bool, deviceRoots *x509.CertPool, responseKey ed25519.PublicKey, expectedTenant string, deviceTrustDER []byte) error {
	return candidateClaimWithInstallPlan(ctx, client, dir, server, code, serial, recoverOnly, deviceRoots, responseKey, expectedTenant, deviceTrustDER, nil)
}

func candidateClaimWithInstallPlan(ctx context.Context, client *http.Client, dir, server, code, serial string, recoverOnly bool, deviceRoots *x509.CertPool, responseKey ed25519.PublicKey, expectedTenant string, deviceTrustDER []byte, targets *candidateInstallTargets) error {
	return candidateClaimOperation(ctx, client, dir, server, code, serial, recoverOnly, deviceRoots, responseKey, expectedTenant, deviceTrustDER, targets, nil)
}
func candidateClaimOperation(ctx context.Context, client *http.Client, dir, server, code, serial string, recoverOnly bool, deviceRoots *x509.CertPool, responseKey ed25519.PublicKey, expectedTenant string, deviceTrustDER []byte, targets *candidateInstallTargets, accept func(*candidateStore, candidateIntent, []byte) error) error {
	if targets != nil && (recoverOnly || !candidateValidInstallTargets(dir, *targets)) {
		return fmt.Errorf("explicit new staging and distinct installer targets required")
	}
	u, e := url.Parse(server)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("verified HTTPS server required")
	}
	server = strings.TrimRight(u.String(), "/")
	if client == nil || code == "" || serial == "" || deviceRoots == nil || len(responseKey) != ed25519.PublicKeySize || expectedTenant == "" || len(deviceTrustDER) == 0 {
		return fmt.Errorf("explicit client, code, serial and device trust required")
	}
	var targetSnapshot map[string]string
	if targets != nil {
		if e = candidateRequireNewDestination(*targets); e != nil {
			return e
		}
		targetSnapshot, e = candidateSnapshotInstallTargets(*targets)
		if e != nil {
			return fmt.Errorf("installed target baseline unavailable; no claim")
		}
	}
	store, e := candidateOpenStore(dir, !recoverOnly)
	if e != nil {
		return e
	}
	defer store.Close()
	if !recoverOnly {
		candidateFaultCheckpoint("after_directory")
	}
	pinDigest, rootsDigest := candidateTrustDigest(responseKey, deviceTrustDER)
	var v candidateIntent
	var key *ecdsa.PrivateKey
	if !recoverOnly {
		key, e = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return e
		}
		raw, _ := x509.MarshalECPrivateKey(key)
		if e = store.Write("identity.key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})); e != nil {
			return e
		}
		candidateFaultCheckpoint("after_key")
		der, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serial}}, key)
		if e != nil {
			return e
		}
		id := make([]byte, 32)
		if _, e = rand.Read(id); e != nil {
			return e
		}
		v = candidateIntent{hex.EncodeToString(id), server, serial, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), expectedTenant, pinDigest, rootsDigest}
		raw, _ = json.Marshal(v)
		if e = store.Write("candidate-intent.json", raw); e != nil {
			return e
		}
		candidateFaultCheckpoint("after_intent")
		if targets != nil {
			digest := sha256.Sum256(raw)
			plan := candidateInstallPlan{Version: 3, Attempt: v.ID, IntentSHA256: hex.EncodeToString(digest[:]), Phase: "prepared", Targets: *targets, TargetSnapshot: targetSnapshot}
			planRaw, e := json.Marshal(plan)
			if e != nil {
				return e
			}
			if e = store.Write("candidate-install-plan.json", planRaw); e != nil {
				return e
			}
			candidateFaultCheckpoint("after_install_plan")
		}
		startedMarker := "uncertain\n"
		if targets != nil {
			startedMarker = "install-uncertain\n"
		}
		if e = store.Write("candidate-started", []byte(startedMarker)); e != nil {
			return e
		}
		candidateFaultCheckpoint("after_started")
		if targets != nil {
			rawPlan, e := store.Read("candidate-install-plan.json")
			if e != nil {
				return e
			}
			var plan candidateInstallPlan
			if candidateDecodeRecovery(rawPlan, &plan) != nil {
				return fmt.Errorf("retained install plan invalid")
			}
			if e = candidateCheckInstallSnapshot(plan); e != nil {
				return e
			}
		}
	} else {
		v, key, e = candidateLoadIntent(store, server, serial, expectedTenant, responseKey, deviceTrustDER)
		if e != nil {
			return e
		}
		// An installer-bound attempt cannot recover through a malformed or
		// partial plan. Recovery preserves its explicit binding, never repairs.
		if e = candidateValidateOptionalInstallPlan(store, v, dir); e != nil {
			return e
		}
		// A partial or mismatched saved response must refuse locally before any
		// request. An exact saved response still needs explicit server recovery to
		// check the server's current active/fingerprint/revocation state.
		saved, readErr := store.Read("candidate-response.json")
		if readErr == nil {
			if e = candidateVerifyResponse(saved, v, key, deviceRoots, responseKey, expectedTenant); e != nil {
				return e
			}
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
	}
	marker, e := store.Read("candidate-started")
	if e != nil || (string(marker) != "uncertain\n" && string(marker) != "install-uncertain\n") {
		return fmt.Errorf("retained started marker invalid")
	}
	var targetGuard *candidateInstallTargetGuard
	if string(marker) == "install-uncertain\n" {
		planRaw, e := store.Read("candidate-install-plan.json")
		if e != nil {
			return e
		}
		var plan candidateInstallPlan
		if candidateDecodeRecovery(planRaw, &plan) != nil {
			return fmt.Errorf("installer plan unavailable")
		}
		targetGuard, e = candidateHoldInstallTargets(plan)
		if e != nil {
			return e
		}
		defer targetGuard.Close()
	}
	signature, e := ecdsa.SignASN1(rand.Reader, key, candidateProof(v, recoverOnly))
	if e != nil {
		return e
	}
	body, _ := json.Marshal(map[string]any{"attempt_id": v.ID, "serial": v.Serial, "csr_pem": v.CSR, "claim_code": code, "signature": signature})
	path := "/candidate/claim"
	if recoverOnly {
		path = "/candidate/recover"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", server+path, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	candidateFaultCheckpoint("before_request")
	resp, e := c.Do(req)
	if e != nil {
		return fmt.Errorf("outcome uncertain; preserve attempt, explicit recover only")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("attempt unavailable; preserve, no replay")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("response invalid")
	}
	if e = candidateVerifyResponse(raw, v, key, deviceRoots, responseKey, expectedTenant); e != nil {
		return e
	}
	if targetGuard != nil {
		if e = targetGuard.Validate(); e != nil {
			return fmt.Errorf("targets changed during server check; preserve retained attempt")
		}
	}
	candidateFaultCheckpoint("after_verified_response")
	old, e := store.Read("candidate-response.json")
	if e == nil {
		if !bytes.Equal(old, raw) {
			return fmt.Errorf("stored response differs")
		}
		if accept != nil {
			return accept(store, v, raw)
		}
		return nil
	}
	if !os.IsNotExist(e) {
		return e
	}
	if e = store.Write("candidate-response.json", raw); e != nil {
		return e
	}
	candidateFaultCheckpoint("after_saved_response")
	if accept != nil {
		return accept(store, v, raw)
	}
	return nil
}
