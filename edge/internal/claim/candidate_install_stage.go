package claim

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
)

// RunCandidateInstallStage creates a new retained attempt and its immutable
// prepared binding before a request. It never accepts into installed targets.
func RunCandidateInstallStage(ctx context.Context, input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded explicit install staging input required")
	}
	var in struct {
		Dir, Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant string
		ResponseKey                                                      ed25519.PublicKey
		Targets                                                          candidateInstallTargets
	}
	if candidateDecodeRecovery(raw, &in) != nil || !candidateStrictInstallTargets(raw) || !candidateValidInstallTargets(in.Dir, in.Targets) {
		return fmt.Errorf("explicit new staging binding and trust required")
	}
	// Reuse strict product creation and network/trust validation, stripping the
	// plan field from its separate fixed shape. Targets are in-memory only until
	// the exclusive store retains key+intent and fsyncs the plan before HTTP.
	request, _ := json.Marshal(struct {
		Dir, Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant string
		ResponseKey                                                      ed25519.PublicKey
	}{in.Dir, in.Server, in.Serial, in.Code, in.TransportCA, in.DeviceCA, in.ExpectedTenant, in.ResponseKey})
	return runCandidateAttemptWithPlan(ctx, bytes.NewReader(request), output, false, &in.Targets)
}
