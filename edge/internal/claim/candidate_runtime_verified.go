package claim

// Private runtime assembly is not system installation. The callback exists only
// for code that keeps service activation and owner-host effects blocked.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type VerifiedDeployment struct {
	Root, State, Unit, ServiceUID, ServiceGID, Activation string
	ParentSnapshot                                        map[string]string
}

type VerifiedPrivateRuntime struct {
	Attempt, IdentityDir, StateDir string
	Files                          map[string][]byte
	Binary                         []byte
	StateHandle                    *os.File
	Validate                       func() error
	Deployment                     *VerifiedDeployment
}

// WithVerifiedPrivateRuntime verifies the creation-bound preparation, refreshes
// the exact retained attempt, and checks the already accepted private identity.
// Its synchronous callback runs while original retained/target handles remain
// held. Those handles and advisory locks are not same-user/root protection.
// This function cannot establish authorization after the callback returns.
func WithVerifiedPrivateRuntime(ctx context.Context, raw []byte, apply func(VerifiedPrivateRuntime) error) error {
	return withVerifiedRuntime(ctx, raw, nil, apply)
}

// WithVerifiedDeploymentRecovery is distinct from private assembly. Its
// read-only inspect callback must classify exact same-attempt frozen payload
// state. It runs before HTTPS and again while verified handles remain held.
// Neither a local receipt nor inspect success authorizes apply before recovery.
// No CLI or installer currently exposes this route.
func WithVerifiedDeploymentRecovery(ctx context.Context, raw []byte, inspect func(VerifiedPrivateRuntime) error, apply func(VerifiedPrivateRuntime) error) error {
	if inspect == nil || apply == nil {
		return fmt.Errorf("explicit deployment inspection and continuation required")
	}
	return withVerifiedRuntime(ctx, raw, inspect, apply)
}
func withVerifiedRuntime(ctx context.Context, raw []byte, inspect func(VerifiedPrivateRuntime) error, apply func(VerifiedPrivateRuntime) error) error {
	var in struct {
		Dir, Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant string
		ResponseKey                                                      ed25519.PublicKey
	}
	if len(raw) > 131072 || candidateDecodeRecovery(raw, &in) != nil || apply == nil || in.Dir == "" || in.Server == "" || in.Serial == "" || in.Code == "" || in.ExpectedTenant == "" || len(in.ResponseKey) != 32 {
		return fmt.Errorf("explicit retained runtime verification required")
	}
	transport, roots := x509.NewCertPool(), x509.NewCertPool()
	if !transport.AppendCertsFromPEM([]byte(in.TransportCA)) || !roots.AppendCertsFromPEM([]byte(in.DeviceCA)) {
		return fmt.Errorf("separate runtime transport and device trust required")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: transport, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	checkSnapshot := candidateCheckInstallSnapshot
	var deploymentCheck func(*candidateStore, candidateIntent, candidateInstallPlan) error
	if inspect != nil {
		checkSnapshot = candidateCheckDeploymentPrivateBaseline
		deploymentCheck = func(store *candidateStore, intent candidateIntent, plan candidateInstallPlan) error {
			if !candidateHasDeployment(plan.Targets) {
				return fmt.Errorf("creation-bound deployment required")
			}
			response, e := store.Read("candidate-response.json")
			if e != nil {
				return e
			}
			return candidateWithVerifiedRuntime(store, intent, response, []byte(in.DeviceCA), checkSnapshot, inspect)
		}
	}
	return candidateClaimCheckedOperation(ctx, client, in.Dir, in.Server, in.Code, in.Serial, true, roots, in.ResponseKey, in.ExpectedTenant, []byte(in.DeviceCA), nil, func(store *candidateStore, intent candidateIntent, response []byte) error {
		return candidateWithVerifiedRuntime(store, intent, response, []byte(in.DeviceCA), checkSnapshot, func(v VerifiedPrivateRuntime) error {
			if inspect != nil {
				base := v.Validate
				v.Validate = func() error {
					if e := base(); e != nil {
						return e
					}
					check := v
					check.Validate = base
					return inspect(check)
				}
				if e := v.Validate(); e != nil {
					return e
				}
			}
			return apply(v)
		})
	}, deploymentCheck)
}

func candidateWithVerifiedRuntime(store *candidateStore, intent candidateIntent, response, deviceCA []byte, checkSnapshot func(candidateInstallPlan) error, apply func(VerifiedPrivateRuntime) error) error {
	marker, e := store.Read("candidate-started")
	if e != nil || string(marker) != "install-uncertain\n" {
		return fmt.Errorf("installer-bound retained attempt required")
	}
	planRaw, e := store.Read("candidate-install-plan.json")
	var plan candidateInstallPlan
	if e != nil || candidateDecodeRecovery(planRaw, &plan) != nil || plan.Version != 3 || plan.Attempt != intent.ID {
		return fmt.Errorf("creation-bound private runtime plan required")
	}
	// Only the product's exact prepared layout is eligible. There is no new
	// caller-selected destination: runtime is a child of prebound prepared-state.
	root := filepath.Dir(plan.Targets.Binary)
	if plan.Targets.Binary != filepath.Join(root, "prepared-agent") || plan.Targets.Config != filepath.Join(root, "prepared-config.yaml") || plan.Targets.Unit != filepath.Join(root, "prepared-unit.service") || plan.Targets.State != filepath.Join(root, "prepared-state") {
		return fmt.Errorf("exact product-prepared private layout required")
	}
	prepared, e := candidateOpenStore(root, false)
	if e != nil {
		return e
	}
	defer prepared.Close()
	binary, e := candidateReadPreparedBinary(prepared)
	if e != nil {
		return e
	}
	var identity Identity
	if json.Unmarshal(response, &identity) != nil {
		return fmt.Errorf("verified runtime identity unavailable")
	}
	key, e := store.Read("identity.key")
	if e != nil {
		return e
	}
	saved, _ := json.Marshal(Identity{GatewayID: identity.GatewayID, IngestToken: identity.IngestToken})
	files := map[string][]byte{"identity.key": key, "identity.json": saved, "identity.crt": []byte(identity.ClientCertPEM), "device-ca.pem": deviceCA}
	parent, e := candidateOpenStore(filepath.Dir(plan.Targets.Destination), false)
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = candidateVerifyAcceptedRuntime(parent, filepath.Base(plan.Targets.Destination), intent.ID, files); e != nil {
		return e
	}
	if e = checkSnapshot(plan); e != nil {
		return e
	}
	// Own-executable equality prevents substituting a different product between
	// preparation and runtime assembly even when a manifest was also replaced.
	own, e := candidateReadOwnExecutable()
	if e != nil || !bytes.Equal(binary, own) {
		return fmt.Errorf("prepared executable differs from running product")
	}
	state, e := candidateOpenRuntimeState(plan.Targets.State)
	if e != nil {
		return e
	}
	defer state.Close()
	validate := func() error {
		if e := checkSnapshot(plan); e != nil {
			return e
		}
		if e := candidateCheckRuntimeState(state, plan.Targets.State); e != nil {
			return e
		}
		return candidateVerifyAcceptedRuntime(parent, filepath.Base(plan.Targets.Destination), intent.ID, files)
	}
	if e = validate(); e != nil {
		return e
	}
	var deployment *VerifiedDeployment
	if candidateHasDeployment(plan.Targets) {
		t := plan.Targets
		deployment = &VerifiedDeployment{Root: t.DeploymentRoot, State: t.DeploymentState, Unit: t.DeploymentUnit, ServiceUID: t.ServiceUID, ServiceGID: t.ServiceGID, Activation: t.Activation, ParentSnapshot: map[string]string{}}
		for _, n := range []string{"deployment_parent", "deployment_state_parent", "deployment_unit_parent"} {
			deployment.ParentSnapshot[n] = plan.TargetSnapshot[n]
		}
	}
	return apply(VerifiedPrivateRuntime{Attempt: intent.ID, IdentityDir: plan.Targets.Destination, StateDir: plan.Targets.State, Files: files, Binary: binary, StateHandle: state, Validate: validate, Deployment: deployment})
}

func candidateCheckDeploymentPrivateBaseline(plan candidateInstallPlan) error {
	if !candidateHasDeployment(plan.Targets) || len(plan.TargetSnapshot) != 10 {
		return fmt.Errorf("complete deployment baseline required")
	}
	t := plan.Targets
	t.DeploymentRoot = ""
	t.DeploymentState = ""
	t.DeploymentUnit = ""
	t.ServiceUID = ""
	t.ServiceGID = ""
	t.Activation = ""
	baseline := map[string]string{}
	for _, n := range []string{"binary", "config", "unit", "identity.key", "identity.json", "state_directory", "destination_parent"} {
		baseline[n] = plan.TargetSnapshot[n]
	}
	private := plan
	private.Targets = t
	private.TargetSnapshot = baseline
	return candidateCheckInstallSnapshot(private)
}
