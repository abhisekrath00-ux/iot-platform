//go:build linux && hex_candidate_fault_acceptance

package setup

import (
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDeploymentAccessChild(t *testing.T) {
	p := os.Getenv("HEX_ACCESS_CHILD")
	if p == "" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("HEX_ACCESS_FIXTURE"))
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		I runtimeBundleIntent
		F map[string][]byte
	}
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("fixture")
	}
	fd, e := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		t.Fatal(e)
	}
	s := &deploymentStore{fd, uint32(os.Getuid())}
	defer s.close()
	tr := &deploymentTransaction{intent: f.I, files: f.F}
	if e = tr.commitAccessPayload(s, uint32(os.Getgid())); e != nil {
		t.Fatal(e)
	}
}
func TestDeploymentAccessActualProcessCuts(t *testing.T) {
	for _, step := range deploymentAccessOrder {
		for _, beat := range []string{"group", "mode"} {
			stage := "access_after_" + beat + "_" + step.Name
			t.Run(stage, func(t *testing.T) {
				tr, s := accessFixture(t)
				fixture := t.TempDir() + "/fixture.json"
				raw, _ := json.Marshal(map[string]any{"I": tr.intent, "F": tr.files})
				if e := os.WriteFile(fixture, raw, 0600); e != nil {
					t.Fatal(e)
				}
				// /proc descriptor resolves the synthetic payload path without inventing it.
				path, e := os.Readlink("/proc/self/fd/" + strconv.Itoa(s.fd))
				if e != nil {
					t.Fatal(e)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestDeploymentAccessChild$")
				cmd.Env = append(os.Environ(), "HEX_ACCESS_CHILD="+path, "HEX_ACCESS_FIXTURE="+fixture, "HEX_CANDIDATE_TEST_CRASH="+stage)
				out, e := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 86 {
					t.Fatal("not intended cut", e, string(out))
				}
				before := runtimeSnapshot(t, path)
				if _, e = tr.inspectAccessPayload(s, uint32(os.Getgid())); e != nil {
					t.Fatal("prefix classification", e)
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, path))) {
					t.Fatal("classifier wrote")
				}
				if e = tr.commitAccessPayload(s, uint32(os.Getgid())); e != nil {
					t.Fatal("recovery", e)
				}
			})
		}
	}
}

func TestStoppedAccessTransactionChild(t *testing.T) {
	root := os.Getenv("HEX_ACCESS_TX_CHILD")
	if root == "" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("HEX_ACCESS_FIXTURE"))
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		I runtimeBundleIntent
		F map[string][]byte
	}
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("fixture")
	}
	v := claim.VerifiedPrivateRuntime{Attempt: f.I.Attempt, StateDir: root + "/retained", Files: map[string][]byte{}, Deployment: f.I.Deployment, Validate: func() error { return nil }}
	for n := range f.I.Identity {
		v.Files[n] = f.F[n]
	}
	e = withDeploymentTransaction(v, f.I, f.F, uint32(os.Getuid()), func(tr *deploymentTransaction) error {
		return tr.commitStoppedAccess(uint32(os.Getuid()), uint32(os.Getgid()))
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestStoppedAccessTransactionActualProcessCuts(t *testing.T) {
	stages := []string{"access_after_intent", "access_after_receipt", "runtime_partial_payload.access.intent.json", "runtime_partial_payload.access.complete.json"}
	for _, step := range deploymentAccessOrder {
		stages = append(stages, "access_after_group_"+step.Name, "access_after_mode_"+step.Name)
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			root := filepath.Dir(v.Deployment.Root)
			if e := runDeploymentTransaction(v, i, f, uid); e != nil {
				t.Fatal(e)
			}
			fixture := root + "/fixture.json"
			raw, _ := json.Marshal(map[string]any{"I": i, "F": f})
			if e := os.WriteFile(fixture, raw, 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestStoppedAccessTransactionChild$")
			cmd.Env = append(os.Environ(), "HEX_ACCESS_TX_CHILD="+root, "HEX_ACCESS_FIXTURE="+fixture, "HEX_CANDIDATE_TEST_CRASH="+stage)
			out, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 86 {
				t.Fatal("not intended cut", e, string(out))
			}
			e = withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				before := runtimeSnapshot(t, root)
				state, e := tr.inspectStoppedAccess(gid)
				partial := strings.HasPrefix(stage, "runtime_partial_")
				if partial && e == nil || !partial && (e != nil || state == "") {
					t.Fatal("classification", state, e)
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
					t.Fatal("classification changed state")
				}
				e = tr.commitStoppedAccess(uid, gid)
				if partial {
					if e == nil {
						t.Fatal("partial repaired")
					}
					if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
						t.Fatal("partial changed")
					}
				} else if e != nil {
					t.Fatal("prefix recovery", e)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
