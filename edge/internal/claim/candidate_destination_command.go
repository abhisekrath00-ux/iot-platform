package claim

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"
)

// Experimental private acceptance only. No system installation or service.
func RunCandidateAcceptPrivateIdentity(ctx context.Context, input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded explicit acceptance input required")
	}
	var in struct {
		Dir, Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant, DestinationParent, DestinationName string
		ResponseKey                                                                                          ed25519.PublicKey
	}
	if candidateDecodeRecovery(raw, &in) != nil || in.Dir == "" || in.Server == "" || in.Code == "" || in.Serial == "" || in.ExpectedTenant == "" || len(in.ResponseKey) != 32 || (in.DestinationName == "" || in.DestinationName == "." || in.DestinationName == ".." || filepath.Base(in.DestinationName) != in.DestinationName) || !filepath.IsAbs(in.DestinationParent) || filepath.Clean(in.DestinationParent) != in.DestinationParent {
		return fmt.Errorf("explicit retained attempt, trust and private destination required")
	}
	// Keep source and destination disjoint; no acceptance over the retained store.
	if !candidateDisjointPaths(in.Dir, in.DestinationParent) {
		return fmt.Errorf("private destination must be separate from retained attempt")
	}
	preflight, e := candidateOpenStore(in.Dir, false)
	if e != nil {
		return fmt.Errorf("retained acceptance binding unavailable")
	}
	planRaw, e := preflight.Read("candidate-install-plan.json")
	preflight.Close()
	var boundPlan candidateInstallPlan
	if e != nil || candidateDecodeRecovery(planRaw, &boundPlan) != nil || boundPlan.Version != 3 || boundPlan.Targets.Destination != filepath.Join(in.DestinationParent, in.DestinationName) {
		return fmt.Errorf("destination differs from pre-enrollment binding; no request")
	}
	transport, roots := x509.NewCertPool(), x509.NewCertPool()
	if !transport.AppendCertsFromPEM([]byte(in.TransportCA)) || !roots.AppendCertsFromPEM([]byte(in.DeviceCA)) {
		return fmt.Errorf("separate transport and device trust required")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: transport, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	e = candidateClaimOperation(ctx, client, in.Dir, in.Server, in.Code, in.Serial, true, roots, in.ResponseKey, in.ExpectedTenant, []byte(in.DeviceCA), nil, func(store *candidateStore, intent candidateIntent, response []byte) error {
		// Callback runs only after fresh exact same-attempt HTTPS recovery, response
		// verification and held-target checks, before the retained handles close.
		marker, e := store.Read("candidate-started")
		if e != nil || string(marker) != "install-uncertain\n" {
			return fmt.Errorf("only explicit installer-bound identity may be accepted")
		}
		planRaw, e := store.Read("candidate-install-plan.json")
		if e != nil {
			return e
		}
		var plan candidateInstallPlan
		if candidateDecodeRecovery(planRaw, &plan) != nil || plan.Version != 3 || plan.Targets.Destination != filepath.Join(in.DestinationParent, in.DestinationName) {
			return fmt.Errorf("destination differs from immutable pre-enrollment binding")
		}
		var identity Identity
		if json.Unmarshal(response, &identity) != nil {
			return fmt.Errorf("verified response unavailable")
		}
		key, e := store.Read("identity.key")
		if e != nil {
			return e
		}
		saved, _ := json.Marshal(Identity{GatewayID: identity.GatewayID, IngestToken: identity.IngestToken})
		files := map[string][]byte{"identity.key": key, "identity.json": saved, "identity.crt": []byte(identity.ClientCertPEM), "device-ca.pem": []byte(in.DeviceCA)}
		return candidateAcceptDestination(in.DestinationParent, in.DestinationName, intent.ID, files)
	})
	if e != nil {
		return fmt.Errorf("private identity acceptance unresolved; preserve source and destination, do not enroll again")
	}
	return json.NewEncoder(output).Encode(CandidateReview{Version: 1, State: "private_identity_accepted_service_blocked", Message: "Exact private identity directory and completion receipt verified after current same-attempt server recovery. No system installation, configuration activation, service start or pending-state clearing occurred."})
}
func candidateDisjointPaths(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, e := filepath.Rel(pair[0], pair[1])
		if e != nil || rel == "." || rel != ".." && !isParentRelative(rel) {
			return false
		}
	}
	return true
}
