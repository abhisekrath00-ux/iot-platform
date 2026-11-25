//go:build linux && hex_candidate_fault_acceptance

package setup

import (
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDeploymentProcessChild(t *testing.T) {
	root := os.Getenv("HEX_DEPLOYMENT_CHILD_ROOT")
	if root == "" {
		return
	}
	raw, e := os.ReadFile(filepath.Join(root, "fixture.json"))
	if e != nil {
		t.Fatal(e)
	}
	var input struct {
		V claim.VerifiedPrivateRuntime
		I runtimeBundleIntent
		F map[string][]byte
	}
	// Function fields are deliberately absent in the serialized fixture.
	var v struct {
		Attempt, StateDir string
		Files             map[string][]byte
		Deployment        *claim.VerifiedDeployment
	}
	var outer struct {
		V json.RawMessage
		I runtimeBundleIntent
		F map[string][]byte
	}
	if json.Unmarshal(raw, &outer) != nil || json.Unmarshal(outer.V, &v) != nil {
		t.Fatal("fixture")
	}
	input.V = claim.VerifiedPrivateRuntime{Attempt: v.Attempt, StateDir: v.StateDir, Files: v.Files, Deployment: v.Deployment, Validate: func() error { return nil }}
	input.I = outer.I
	input.F = outer.F
	if e = runDeploymentTransaction(input.V, input.I, input.F, uint32(os.Getuid())); e != nil {
		t.Fatal(e)
	}
}
func TestDeploymentActualProcessCuts(t *testing.T) {
	stages := []string{"deploy_after_intent", "deploy_after_directory", "deploy_after_identity_directory", "deploy_after_promotion", "deploy_after_receipt", "runtime_partial_payload.install.intent.json", "runtime_partial_payload.install.complete.json"}
	for _, n := range deploymentOrder {
		stages = append(stages, "deploy_after_"+n, "runtime_partial_"+n)
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			root := filepath.Dir(v.Deployment.Root)
			raw, _ := json.Marshal(map[string]any{"V": map[string]any{"Attempt": v.Attempt, "StateDir": v.StateDir, "Files": v.Files, "Deployment": v.Deployment}, "I": i, "F": f})
			if e := os.WriteFile(filepath.Join(root, "fixture.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestDeploymentProcessChild$")
			cmd.Env = append(os.Environ(), "HEX_DEPLOYMENT_CHILD_ROOT="+root, "HEX_CANDIDATE_TEST_CRASH="+stage)
			out, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 86 {
				t.Fatal("not intended cut", e, string(out))
			}
			before := runtimeSnapshot(t, root)
			state, inspectErr := inspectDeploymentTransaction(v, i, f, uint32(os.Getuid()))
			if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
				t.Fatal("inspection wrote state")
			}
			partial := len(stage) > 16 && stage[:16] == "runtime_partial_"
			if partial && inspectErr == nil || !partial && (inspectErr != nil || state == "") {
				t.Fatal("wrong prefix classification", state, inspectErr)
			}
			e = runDeploymentTransaction(v, i, f, uint32(os.Getuid()))
			if len(stage) > 16 && stage[:16] == "runtime_partial_" {
				if e == nil {
					t.Fatal("partial reconciled")
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
					t.Fatal("partial changed")
				}
			} else if e != nil {
				t.Fatal("complete prefix recovery", e)
			}
		})
	}
}
