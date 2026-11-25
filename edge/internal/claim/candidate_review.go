package claim

import (
	"crypto/ed25519"
	"crypto/x509"
	"os"
)

// CandidateReview is read-only local evidence, never authority to install,
// clear an installer journal, enroll, recover over network or start service.
type CandidateReview struct {
	Version         int    `json:"version"`
	State           string `json:"state"`
	CanInstall      bool   `json:"can_install"`
	CanStart        bool   `json:"can_start"`
	CanClearPending bool   `json:"can_clear_pending"`
	Message         string `json:"message"`
}

func ReviewCandidateAttempt(dir, server, serial, tenant string, roots *x509.CertPool, pin ed25519.PublicKey, deviceTrustDER []byte) (CandidateReview, error) {
	out := CandidateReview{Version: 1, State: "unresolved", Message: "Keep saved identity and pending installation. Review did not contact the server or change files."}
	store, e := candidateOpenStore(dir, false)
	if e != nil {
		return out, e
	}
	defer store.Close()
	v, key, e := candidateLoadIntent(store, server, serial, tenant, pin, deviceTrustDER)
	if e != nil {
		return out, e
	}
	if e = candidateValidateOptionalInstallPlan(store, v, dir); e != nil {
		return out, e
	}
	raw, e := store.Read("candidate-response.json")
	if os.IsNotExist(e) {
		out.State = "outcome_uncertain"
		out.Message = "The claim outcome is uncertain. Keep the saved key and claim code. No enrollment retry or service start is allowed. Explicit same-attempt server recovery is a separate action."
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if e = candidateVerifyResponse(raw, v, key, roots, pin, tenant); e != nil {
		return out, e
	}
	out.State = "verified_saved_response"
	out.Message = "A signed saved response matches this attempt, server trust and retained key. This read-only check cannot confirm current server revocation or finish installation. Pending installation stays blocked."
	return out, nil
}
