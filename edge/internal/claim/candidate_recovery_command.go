package claim

// Explicit retained-attempt adapters. Commands select create or recover,
// never a JSON operation switch. No installer, service controller or pending-
// state clearing is available through these interfaces.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func RunCandidateRecovery(ctx context.Context, input io.Reader, output io.Writer) error {
	return runCandidateAttemptCommand(ctx, input, output, true)
}

// RunCandidateEnrollment is an explicit new-attempt command. Existing or
// partial state refuses before HTTP. It has no installer/service capability.
func RunCandidateEnrollment(ctx context.Context, input io.Reader, output io.Writer) error {
	return runCandidateAttemptCommand(ctx, input, output, false)
}

func runCandidateAttemptCommand(ctx context.Context, input io.Reader, output io.Writer, recoverOnly bool) error {
	return runCandidateAttemptWithPlan(ctx, input, output, recoverOnly, nil)
}
func runCandidateAttemptWithPlan(ctx context.Context, input io.Reader, output io.Writer, recoverOnly bool, targets *candidateInstallTargets) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded recovery input required")
	}
	var v struct {
		Dir, Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant string
		ResponseKey                                                      ed25519.PublicKey
	}
	if candidateDecodeRecovery(raw, &v) != nil {
		return fmt.Errorf("invalid explicit recovery input")
	}
	if v.Dir == "" || v.Server == "" || v.Serial == "" || v.Code == "" || v.ExpectedTenant == "" || len(v.ResponseKey) != ed25519.PublicKeySize {
		return fmt.Errorf("explicit retained attempt and trust required")
	}
	transport, device := x509.NewCertPool(), x509.NewCertPool()
	if !transport.AppendCertsFromPEM([]byte(v.TransportCA)) || !device.AppendCertsFromPEM([]byte(v.DeviceCA)) {
		return fmt.Errorf("separate transport and device trust required")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: transport, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	// Operation is fixed by the selected product command, never a JSON field.
	if e = candidateClaimWithInstallPlan(ctx, client, v.Dir, v.Server, v.Code, v.Serial, recoverOnly, device, v.ResponseKey, v.ExpectedTenant, []byte(v.DeviceCA), targets); e != nil {
		return fmt.Errorf("explicit retained attempt not completed; preserve identity and pending installation, do not replay enrollment")
	}
	// CandidateClaim verified and fsynced the result using its pinned store.
	// Do not reopen a pathname here: it may have been renamed/replaced while
	// the response arrived. A later review is a separate read-only operation.
	state := "verified_recovery_retained"
	if !recoverOnly {
		state = "verified_new_attempt_retained"
		if targets != nil {
			state = "verified_install_attempt_retained"
		}
	}
	out := CandidateReview{Version: 1, State: state, Message: "The explicit attempt response was verified and retained. Keep pending installation blocked. No identity install or service start was performed; current server authorization still needs a separate check."}
	return json.NewEncoder(output).Encode(out)
}

func candidateDecodeRecovery(raw []byte, target any) error {
	scan := json.NewDecoder(bytes.NewReader(raw))
	token, e := scan.Token()
	if e != nil || token != json.Delim('{') {
		return fmt.Errorf("object required")
	}
	canonical, e := json.Marshal(target)
	if e != nil {
		return e
	}
	var allowed map[string]json.RawMessage
	if json.Unmarshal(canonical, &allowed) != nil {
		return fmt.Errorf("object type required")
	}
	seen := map[string]bool{}
	for scan.More() {
		token, e = scan.Token()
		name, ok := token.(string)
		if e != nil || !ok || seen[name] {
			return fmt.Errorf("duplicate field")
		}
		if _, ok = allowed[name]; !ok {
			return fmt.Errorf("unknown field")
		}
		seen[name] = true
		var value json.RawMessage
		if scan.Decode(&value) != nil {
			return fmt.Errorf("invalid field")
		}
	}
	if _, e = scan.Token(); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(target); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("single input required")
	}
	return nil
}
