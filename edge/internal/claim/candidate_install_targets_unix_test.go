//go:build !windows

package claim

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCandidateInstallTargetSnapshots(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	targets := candidateInstallTargets{Binary: filepath.Join(parent, "bin/agent"), Config: filepath.Join(parent, "etc/config"), State: filepath.Join(parent, "state"), Unit: filepath.Join(parent, "unit/service"), Destination: filepath.Join(parent, "accepted")}
	for _, p := range []string{targets.Binary, targets.Config, targets.Unit} {
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte("synthetic baseline"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.MkdirAll(targets.State, 0700); e != nil {
		t.Fatal(e)
	}
	baseline, e := candidateSnapshotInstallTargets(targets)
	if e != nil || len(baseline) != 7 {
		t.Fatal(e, baseline)
	}
	again, e := candidateSnapshotInstallTargets(targets)
	if e != nil || !reflect.DeepEqual(baseline, again) {
		t.Fatal("unchanged targets differ", e)
	}
	plan := candidateInstallPlan{TargetSnapshot: baseline, Targets: targets}
	if e = candidateCheckInstallSnapshot(plan); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"identity.key", "identity.json", "identity.crt", "candidate-started"} {
		p := filepath.Join(targets.State, name)
		if e = os.WriteFile(p, []byte("existing or partial"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = candidateSnapshotInstallTargets(targets); e == nil {
			t.Fatal("existing or partial identity accepted", name)
		}
		if e = os.Remove(p); e != nil {
			t.Fatal(e)
		}
	}
	guard, e := candidateHoldInstallTargets(plan)
	if e != nil {
		t.Fatal(e)
	}
	defer guard.Close()
	configParent := filepath.Dir(targets.Config)
	moved := configParent + "-moved"
	if e = os.Rename(configParent, moved); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(configParent, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Link(filepath.Join(moved, filepath.Base(targets.Config)), targets.Config); e != nil {
		t.Fatal(e)
	}
	if guard.Validate() == nil {
		t.Fatal("replaced parent accepted")
	}
	if e = os.Remove(targets.Config); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(configParent); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(moved, configParent); e != nil {
		t.Fatal(e)
	}
	if e = guard.Validate(); e != nil {
		t.Fatal("restored parent refused", e)
	}
	if e = os.WriteFile(targets.Config, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if candidateCheckInstallSnapshot(plan) == nil {
		t.Fatal("changed target accepted")
	}
	if e = os.Remove(targets.Config); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(targets.Binary, targets.Config); e != nil {
		t.Fatal(e)
	}
	if _, e = candidateSnapshotInstallTargets(targets); e == nil {
		t.Fatal("symlink target accepted")
	}
	if e = os.Remove(targets.Config); e != nil {
		t.Fatal(e)
	}
	if e = os.Link(targets.Binary, targets.Config); e != nil {
		t.Fatal(e)
	}
	if _, e = candidateSnapshotInstallTargets(targets); e == nil {
		t.Fatal("hardlink target accepted")
	}
}

func TestCandidateInstallTargetRefusesWritableOrLinkedAncestors(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	safe := filepath.Join(parent, "safe")
	if e := os.Mkdir(safe, 0700); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(parent, "link")
	if e := os.Symlink(safe, link); e != nil {
		t.Fatal(e)
	}
	if fd, e := candidateOpenTargetDirectory(link); e == nil {
		os.NewFile(uintptr(fd), "bad").Close()
		t.Fatal("linked ancestor accepted")
	}
	if e := os.Chmod(safe, 0777); e != nil {
		t.Fatal(e)
	}
	if fd, e := candidateOpenTargetDirectory(safe); e == nil {
		os.NewFile(uintptr(fd), "bad").Close()
		t.Fatal("writable leaf directory accepted")
	}
}
