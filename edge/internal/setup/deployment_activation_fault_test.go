//go:build linux && hex_candidate_fault_acceptance

package setup

import (
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type activationFixtureInput struct {
	I runtimeBundleIntent
	F map[string][]byte
	A deploymentActivation
}

func TestActivationProcessChild(t *testing.T) {
	path := os.Getenv("HEX_ACTIVATION_FIXTURE")
	if path == "" {
		return
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var in activationFixtureInput
	if json.Unmarshal(raw, &in) != nil {
		t.Fatal("fixture")
	}
	i, f, a := in.I, in.F, in.A
	v := claim.VerifiedPrivateRuntime{Attempt: i.Attempt, StateDir: filepath.Dir(path) + "/retained", Deployment: i.Deployment, Files: map[string][]byte{}, Validate: func() error { return nil }}
	for n := range i.Identity {
		v.Files[n] = f[n]
	}
	e = withDeploymentTransaction(v, i, f, uint32(os.Getuid()), func(tr *deploymentTransaction) error {
		if e := tr.startActivation(uint32(os.Getuid()), uint32(os.Getuid()), uint32(os.Getgid()), a, func() error {
			if e := os.WriteFile(filepath.Dir(path)+"/callback-ran", []byte("once"), 0600); e != nil {
				return e
			}
			return os.WriteFile(filepath.Join(v.Deployment.State, "queue.db"), []byte("runtime"), 0600)
		}); e != nil {
			return e
		}
		return tr.completeActivation(uint32(os.Getuid()), uint32(os.Getuid()), uint32(os.Getgid()), a, func() (bool, deploymentMeasurement, error) {
			return true, deploymentMeasurement{EventID: "new-receipt", Tenant: a.Tenant, Gateway: a.Gateway, Device: a.Device, Point: a.Point, Value: a.Value, Quality: "measured", ReceivedUnixNano: a.BeganUnixNano + 1}, nil
		})
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestActivationActualProcessCutsNeverReplayUncertainStart(t *testing.T) {
	stages := []string{"activation_after_intent", "activation_before_start", "activation_after_start", "activation_after_receipt", "runtime_partial_payload.activation.intent.json", "runtime_partial_payload.activation.start.json", "runtime_partial_payload.activation.complete.json"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			root := filepath.Dir(v.Deployment.Root)
			var a deploymentActivation
			e := withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				if e := tr.commitStoppedDeployment(uid, uid, gid); e != nil {
					return e
				}
				var e error
				a, e = tr.activationExpected("tenant", "gateway", "device", "point", 110, time.Now())
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			fixture := root + "/fixture.json"
			raw, _ := json.Marshal(activationFixtureInput{i, f, a})
			if e = os.WriteFile(fixture, raw, 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestActivationProcessChild$")
			cmd.Env = append(os.Environ(), "HEX_ACTIVATION_FIXTURE="+fixture, "HEX_CANDIDATE_TEST_CRASH="+stage)
			out, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 86 {
				t.Fatal("not intended processcut", e, string(out))
			}
			e = withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				before := runtimeSnapshot(t, root)
				loaded, phase, err := tr.loadActivation()
				partial := strings.HasPrefix(stage, "runtime_partial_")
				if partial {
					if err == nil {
						t.Fatal("partial accepted", phase)
					}
					if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
						t.Fatal("partial repaired")
					}
					return nil
				}
				if err != nil {
					t.Fatal("load exact activation", err)
				}
				x, _ := json.Marshal(a)
				y, _ := json.Marshal(loaded)
				if string(x) != string(y) {
					t.Fatal("timestamp/target changed during load")
				}
				calls := 0
				if phase == "activation_prepared_no_start" {
					if err = tr.startActivation(uid, uid, gid, loaded, func() error { calls++; return nil }); err != nil || calls != 1 {
						t.Fatal("prepared safe resume", err, calls)
					}
				} else {
					if err = tr.startActivation(uid, uid, gid, loaded, func() error { calls++; return nil }); err == nil || calls != 0 {
						t.Fatal("uncertain start replayed", err, calls)
					}
				}
				if stage == "activation_before_start" {
					if _, err = os.Stat(root + "/callback-ran"); !os.IsNotExist(err) {
						t.Fatal("prestartcut callback ran")
					}
				}
				// Simulated live exact status plus independent matching receipt, never a start.
				if err = tr.completeActivation(uid, uid, gid, loaded, func() (bool, deploymentMeasurement, error) {
					return true, deploymentMeasurement{EventID: "reconciled-receipt", Tenant: a.Tenant, Gateway: a.Gateway, Device: a.Device, Point: a.Point, Value: a.Value, Quality: "measured", ReceivedUnixNano: a.BeganUnixNano + 2}, nil
				}); err != nil {
					t.Fatal("live evidence reconciliation", err)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
