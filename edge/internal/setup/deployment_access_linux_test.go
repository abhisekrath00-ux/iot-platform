//go:build linux

package setup

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func accessFixture(t *testing.T) (*deploymentTransaction, *deploymentStore) {
	t.Helper()
	v, i, f := deploymentFixture(t)
	if e := runDeploymentTransaction(v, i, f, uint32(os.Getuid())); e != nil {
		t.Fatal(e)
	}
	p, e := openDeploymentParent(filepath.Dir(v.Deployment.Root), uint32(os.Getuid()))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.close)
	s, e := p.dir(filepath.Base(v.Deployment.Root))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.close)
	return &deploymentTransaction{intent: i, files: f}, s
}
func TestDeploymentAccessExactReadOnlyPrefixAndCommit(t *testing.T) {
	tr, s := accessFixture(t)
	gid := uint32(os.Getgid())
	prefix, e := tr.inspectAccessPayload(s, gid)
	if e != nil || prefix != 0 {
		t.Fatal(prefix, e)
	}
	for repeat := 0; repeat < 2; repeat++ {
		if e = tr.commitAccessPayload(s, gid); e != nil {
			t.Fatal(e)
		}
	}
	prefix, e = tr.inspectAccessPayload(s, gid)
	if e != nil || prefix != 20 {
		t.Fatal(prefix, e)
	}
	for _, step := range deploymentAccessOrder {
		fd, e := deploymentOpenAccessAt(s, step)
		if e != nil {
			t.Fatal(e)
		}
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		unix.Close(fd)
		if e != nil || st.Uid != uint32(os.Getuid()) || st.Gid != gid || st.Mode&07777 != step.Mode || st.Mode&0022 != 0 {
			t.Fatal("root-owned service-read-only payload", step.Name, e)
		}
	}
}
func TestDeploymentAccessRefusesUnknownOrGap(t *testing.T) {
	for _, kind := range []string{"write", "worldread", "setuid", "gap", "changed-key", "symlink", "extra", "wrong-kind"} {
		t.Run(kind, func(t *testing.T) {
			tr, s := accessFixture(t)
			fd, e := deploymentOpenAccessAt(s, deploymentAccessOrder[1])
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "write":
				unix.Fchmod(fd, 0660)
			case "worldread":
				unix.Fchmod(fd, 0644)
			case "setuid":
				unix.Fchmod(fd, 04750)
			case "gap":
				unix.Fchmod(fd, 0640)
			case "changed-key":
				w, e := unix.Openat(s.fd, "config.yaml", unix.O_WRONLY|unix.O_NOFOLLOW, 0)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = unix.Pwrite(w, []byte("changed"), 0); e != nil {
					t.Fatal(e)
				}
				unix.Close(w)
			case "symlink":
				unix.Unlinkat(s.fd, "config.yaml", 0)
				unix.Symlinkat("agent", s.fd, "config.yaml")
			case "extra":
				unix.Mkdirat(s.fd, "unknown", 0700)
			case "wrong-kind":
				unix.Unlinkat(s.fd, "config.yaml", 0)
				unix.Mkdirat(s.fd, "config.yaml", 0700)
			}
			unix.Close(fd)
			if _, e = tr.inspectAccessPayload(s, uint32(os.Getgid())); e == nil {
				t.Fatal("unsafe access accepted")
			}
		})
	}
}

func TestStoppedAccessDurableIntentReceiptExactAndRepeat(t *testing.T) {
	for _, kind := range []string{"valid", "partial-intent", "partial-receipt", "foreign-receipt", "missing-payload-receipt", "unknown-unit"} {
		t.Run(kind, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			if e := runDeploymentTransaction(v, i, f, uid); e != nil {
				t.Fatal(e)
			}
			e := withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				p := tr.parents["deployment_parent"]
				leaf := tr.leaves["deployment_parent"]
				if kind == "partial-intent" {
					os.WriteFile(v.Deployment.Root+".access.intent.json", []byte("{"), 0600)
				} else {
					if e := tr.commitStoppedAccess(uid, gid); e != nil {
						return e
					}
				}
				switch kind {
				case "partial-receipt":
					os.WriteFile(v.Deployment.Root+".access.complete.json", []byte("{"), 0600)
				case "foreign-receipt":
					os.WriteFile(v.Deployment.Root+".access.complete.json", []byte("foreign"), 0600)
				case "missing-payload-receipt":
					unix.Unlinkat(p.fd, leaf+".install.complete.json", 0)
				case "unknown-unit":
					os.WriteFile(v.Deployment.Unit, []byte("unknown"), 0600)
				}
				before := runtimeSnapshot(t, filepath.Dir(v.Deployment.Root))
				state, e := tr.inspectStoppedAccess(gid)
				if kind == "valid" {
					if e != nil || state != "complete_stopped_service_access" {
						t.Fatal(state, e)
					}
					if e = tr.commitStoppedAccess(uid, gid); e != nil {
						t.Fatal("repeat", e)
					}
				} else if e == nil {
					t.Fatal("partial or foreign access state accepted", state)
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, filepath.Dir(v.Deployment.Root)))) {
					t.Fatal("inspection/repeat changed bytes")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
