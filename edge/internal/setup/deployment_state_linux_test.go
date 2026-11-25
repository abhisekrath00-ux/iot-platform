//go:build linux

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWritableStateBoundInodeCommitAndRefusals(t *testing.T) {
	for _, kind := range []string{"valid", "occupied", "unknown-owner", "partial-intent", "partial-inode", "partial-receipt", "replaced", "contents", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			root := filepath.Dir(v.Deployment.State)
			e := withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				if kind == "occupied" {
					os.Mkdir(v.Deployment.State, 0700)
				} else if kind == "symlink" {
					os.Symlink("absent", v.Deployment.State)
				} else {
					if e := tr.commitWritableState(uid, uid, gid); e != nil {
						return e
					}
				}
				switch kind {
				case "partial-intent":
					os.WriteFile(v.Deployment.State+".state.intent.json", []byte("{"), 0600)
				case "partial-inode":
					os.WriteFile(v.Deployment.State+".state.inode.json", []byte("{"), 0600)
				case "partial-receipt":
					os.WriteFile(v.Deployment.State+".state.complete.json", []byte("{"), 0600)
				case "replaced":
					os.Rename(v.Deployment.State, v.Deployment.State+"-old")
					os.Mkdir(v.Deployment.State, 0700)
				case "contents":
					os.WriteFile(filepath.Join(v.Deployment.State, "unknown"), []byte("x"), 0600)
				case "unknown-owner":
					os.Chmod(v.Deployment.State, 0750)
				}
				before := runtimeSnapshot(t, root)
				state, e := tr.inspectWritableState(uid, gid)
				if kind == "valid" {
					if e != nil || state != "state_complete_empty" {
						t.Fatal(state, e)
					}
					if e = tr.commitWritableState(uid, uid, gid); e != nil {
						t.Fatal(e)
					}
				} else if e == nil {
					t.Fatal("unknown writable state accepted", state)
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
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
