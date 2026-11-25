//go:build linux

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoppedCompositionCompleteAndEarlierPrerequisiteRefusal(t *testing.T) {
	for _, kind := range []string{"valid", "state-before-access", "foreign-state", "partial-access-receipt", "changed-key", "foreign-unit", "partial-unit-receipt", "partial-state-inode"} {
		t.Run(kind, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			e := withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				if kind == "state-before-access" {
					if e := tr.commitWritableState(uid, uid, gid); e != nil {
						return e
					}
				} else {
					if e := tr.commitStoppedDeployment(uid, uid, gid); e != nil {
						return e
					}
				}
				switch kind {
				case "foreign-state":
					os.WriteFile(filepath.Join(v.Deployment.State, "unknown"), []byte("x"), 0600)
				case "partial-access-receipt":
					os.WriteFile(v.Deployment.Root+".access.complete.json", []byte("{"), 0600)
				case "foreign-unit":
					os.WriteFile(v.Deployment.Unit, []byte("foreign"), 0600)
				case "partial-unit-receipt":
					os.WriteFile(v.Deployment.Unit+".unit.complete.json", []byte("{"), 0600)
				case "partial-state-inode":
					os.WriteFile(v.Deployment.State+".state.inode.json", []byte("{"), 0600)
				case "changed-key":
					os.WriteFile(filepath.Join(v.Deployment.Root, "identity/identity.key"), []byte("other"), 0640)
				}
				before := runtimeSnapshot(t, filepath.Dir(v.Deployment.Root))
				state, e := tr.inspectStoppedDeployment(uid, gid)
				if kind == "valid" {
					if e != nil || state != "unit_complete_stopped" {
						t.Fatal(state, e)
					}
					if e = tr.commitStoppedDeployment(uid, uid, gid); e != nil {
						t.Fatal("repeat", e)
					}
				} else if e == nil {
					t.Fatal("earlier prerequisite bypassed", state)
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, filepath.Dir(v.Deployment.Root)))) {
					t.Fatal("inspection/repeat changed state")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
