//go:build linux && hex_candidate_fault_acceptance

package setup

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"testing"
)

func TestRuntimeBundleChild(t *testing.T) {
	p := os.Getenv("HEX_RUNTIME_CHILD_ROOT")
	if p == "" {
		return
	}
	fd, e := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		t.Fatal(e)
	}
	s := &runtimeBundleStore{fd}
	defer s.close()
	i, f := runtimeFixtureData()
	bundle := "runtime"
	if os.Getenv("HEX_RUNTIME_CHILD_BUNDLE") == "handoff" {
		bundle = "handoff"
		i, f = handoffFixtureData()
	}
	if e = commitNamedRuntimeBundle(s, i, f, bundle); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeBundleActualProcessCuts(t *testing.T) {
	for _, stage := range []string{"runtime_after_intent", "runtime_after_directory", "runtime_after_agent", "runtime_after_config.yaml", "runtime_after_unit.service", "runtime_after_promotion", "runtime_after_receipt", "runtime_partial_runtime.intent.json", "runtime_partial_agent", "runtime_partial_config.yaml", "runtime_partial_unit.service", "runtime_partial_runtime.complete.json"} {
		t.Run(stage, func(t *testing.T) {
			p, s, i, f := runtimeFixture(t)
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeBundleChild$")
			cmd.Env = append(os.Environ(), "HEX_RUNTIME_CHILD_ROOT="+p, "HEX_CANDIDATE_TEST_CRASH="+stage)
			if out, e := cmd.CombinedOutput(); e == nil {
				t.Fatal("missing process cut", string(out))
			} else {
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 86 {
					t.Fatal("not intended process cut", e, string(out))
				}
			}
			before := runtimeSnapshot(t, p)
			e := commitRuntimeBundle(s, i, f)
			partial := len(stage) > 16 && stage[:16] == "runtime_partial_"
			if partial {
				if e == nil {
					t.Fatal("partial bytes reconciled")
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, p))) {
					t.Fatal("partial bytes changed")
				}
			} else if e != nil {
				t.Fatal("exact complete prefix recovery", e)
			}
		})
	}
}

func TestHandoffActualProcessCuts(t *testing.T) {
	stages := []string{"handoff_after_intent", "handoff_after_directory", "handoff_after_promotion", "handoff_after_receipt", "runtime_partial_handoff.intent.json", "runtime_partial_handoff.complete.json"}
	for _, n := range []string{"agent", "config.yaml", "unit.service", "broker-ca.pem", "device-ca.pem", "identity.json", "identity.crt", "identity.key"} {
		stages = append(stages, "handoff_after_"+n, "runtime_partial_"+n)
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			p, s, _, _ := runtimeFixture(t)
			i, f := handoffFixtureData()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeBundleChild$")
			cmd.Env = append(os.Environ(), "HEX_RUNTIME_CHILD_ROOT="+p, "HEX_RUNTIME_CHILD_BUNDLE=handoff", "HEX_CANDIDATE_TEST_CRASH="+stage)
			out, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 86 {
				t.Fatal("not intended cut", e, string(out))
			}
			before := runtimeSnapshot(t, p)
			e = commitNamedRuntimeBundle(s, i, f, "handoff")
			if len(stage) > 16 && stage[:16] == "runtime_partial_" {
				if e == nil {
					t.Fatal("partial repaired")
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, p))) {
					t.Fatal("partial changed")
				}
			} else if e != nil {
				t.Fatal("complete prefix recovery", e)
			}
		})
	}
}
