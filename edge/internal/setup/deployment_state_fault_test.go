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
)

func TestWritableStateChild(t *testing.T) {
	path := os.Getenv("HEX_STATE_FIXTURE")
	if path == "" {
		return
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var input struct {
		I runtimeBundleIntent
		F map[string][]byte
	}
	if json.Unmarshal(raw, &input) != nil {
		t.Fatal("fixture")
	}
	i, f := input.I, input.F
	v := claim.VerifiedPrivateRuntime{Attempt: i.Attempt, StateDir: filepath.Dir(path) + "/retained", Deployment: i.Deployment, Files: map[string][]byte{}, Validate: func() error { return nil }}
	for n := range i.Identity {
		v.Files[n] = f[n]
	}
	e = withDeploymentTransaction(v, i, f, uint32(os.Getuid()), func(tr *deploymentTransaction) error {
		return tr.commitWritableState(uint32(os.Getuid()), uint32(os.Getuid()), uint32(os.Getgid()))
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestWritableStateActualProcessCuts(t *testing.T) {
	for _, stage := range []string{"state_after_intent", "state_after_directory", "state_after_inode", "state_after_ownership", "state_after_promotion", "state_after_receipt", "runtime_partial_state.state.intent.json", "runtime_partial_state.state.inode.json", "runtime_partial_state.state.complete.json"} {
		t.Run(stage, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			root := filepath.Dir(v.Deployment.Root)
			fixture := root + "/fixture.json"
			raw, _ := json.Marshal(map[string]any{"I": i, "F": f})
			if e := os.WriteFile(fixture, raw, 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestWritableStateChild$")
			cmd.Env = append(os.Environ(), "HEX_STATE_FIXTURE="+fixture, "HEX_CANDIDATE_TEST_CRASH="+stage)
			out, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 86 {
				t.Fatal("not intended cut", e, string(out))
			}
			e = withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				before := runtimeSnapshot(t, root)
				state, e := tr.inspectWritableState(uid, gid)
				partial := strings.HasPrefix(stage, "runtime_partial_")
				if partial && e == nil || !partial && (e != nil || state == "") {
					t.Fatal("classification", state, e)
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
					t.Fatal("classification wrote")
				}
				e = tr.commitWritableState(uid, uid, gid)
				if partial {
					if e == nil {
						t.Fatal("partial repaired")
					}
					if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
						t.Fatal("partial changed")
					}
				} else if e != nil {
					t.Fatal("exactprefix recovery", e)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
