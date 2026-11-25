//go:build linux

package setup

import (
	"bytes"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"os"
	"path/filepath"
	"testing"
)

func deploymentFixture(t *testing.T) (claim.VerifiedPrivateRuntime, runtimeBundleIntent, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	i, f := handoffFixtureData()
	d := i.Deployment
	d.Root = filepath.Join(root, "payload")
	d.State = filepath.Join(root, "state")
	d.Unit = filepath.Join(root, "hex.service")
	p, e := openDeploymentParent(root, uint32(os.Getuid()))
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	for n := range d.ParentSnapshot {
		d.ParentSnapshot[n] = p.snapshot()
	}
	v := claim.VerifiedPrivateRuntime{Attempt: i.Attempt, StateDir: filepath.Join(root, "retained-state"), Files: map[string][]byte{}, Deployment: d, Validate: func() error { return nil }}
	for n := range i.Identity {
		v.Files[n] = f[n]
	}
	return v, i, f
}
func TestDeploymentRootGateAndPrivateNestedCommit(t *testing.T) {
	v, i, f := deploymentFixture(t)
	if os.Geteuid() != 0 && rootDeploymentTransaction(v, i, f) == nil {
		t.Fatal("nonroot accepted")
	}
	for repeat := 0; repeat < 2; repeat++ {
		if e := runDeploymentTransaction(v, i, f, uint32(os.Getuid())); e != nil {
			t.Fatal(e)
		}
	}
	for _, n := range deploymentOrder {
		p := filepath.Join(v.Deployment.Root, n)
		if n == "device-ca.pem" || len(n) > 9 && n[:9] == "identity." {
			p = filepath.Join(v.Deployment.Root, "identity", n)
		}
		b, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(b, f[n]) {
			t.Fatal(n, e)
		}
		st, e := os.Stat(p)
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("private nonexec", n, e)
		}
	}
	for _, p := range []string{v.Deployment.State, v.Deployment.Unit} {
		if _, e := os.Lstat(p); !os.IsNotExist(e) {
			t.Fatal("stopped freeze touched state/unit", e)
		}
	}
	i.Files["agent"] = runtimeHash([]byte("changed"))
	f["agent"] = []byte("changed")
	if runDeploymentTransaction(v, i, f, uint32(os.Getuid())) == nil {
		t.Fatal("changed frozen bytes accepted")
	}
}
func TestDeploymentRefusesUnsafeUnboundOrChangedState(t *testing.T) {
	for _, kind := range []string{"occupied-root", "occupied-state", "occupied-unit", "symlink-parent", "changed-parent", "partial-intent", "bad-authority", "bad-key", "overlap", "extra", "prefix-gap", "partial-file", "extra-file", "symlink-file", "hardlink-file", "mode", "partial-receipt"} {
		t.Run(kind, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			d := v.Deployment
			root := filepath.Dir(d.Root)
			write := func(p string, b []byte) {
				t.Helper()
				if e := os.WriteFile(p, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "occupied-root":
				write(d.Root, []byte("other"))
			case "occupied-state":
				write(d.State, []byte("other"))
			case "occupied-unit":
				write(d.Unit, []byte("other"))
			case "symlink-parent":
				if e := os.Rename(root, root+"-moved"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(root+"-moved", root); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { os.Remove(root); os.Rename(root+"-moved", root) })
			case "changed-parent":
				d.ParentSnapshot["deployment_parent"] = "wrong"
			case "partial-intent":
				write(d.Root+".install.intent.json", []byte("{"))
			case "bad-authority":
				v.Validate = func() error { return os.ErrPermission }
			case "bad-key":
				f["identity.key"] = []byte("changed")
				i.Files["identity.key"] = runtimeHash(f["identity.key"])
			case "overlap":
				d.State = filepath.Join(d.Root, "state")
			case "extra":
				f["extra"] = []byte("x")
			default:
				if e := runDeploymentTransaction(v, i, f, uint32(os.Getuid())); e != nil {
					t.Fatal(e)
				}
				switch kind {
				case "prefix-gap":
					os.Rename(d.Root, d.Root+".install.pending")
					os.Remove(d.Root + ".install.complete.json")
					os.Remove(filepath.Join(d.Root+".install.pending", "agent"))
				case "partial-file":
					write(filepath.Join(d.Root, "identity", "identity.key"), []byte("partial"))
				case "extra-file":
					write(filepath.Join(d.Root, "unknown"), []byte("other"))
				case "symlink-file":
					os.Remove(filepath.Join(d.Root, "agent"))
					os.Symlink("config.yaml", filepath.Join(d.Root, "agent"))
				case "hardlink-file":
					os.Remove(filepath.Join(d.Root, "agent"))
					os.Link(filepath.Join(d.Root, "config.yaml"), filepath.Join(d.Root, "agent"))
				case "mode":
					os.Chmod(filepath.Join(d.Root, "agent"), 0700)
				case "partial-receipt":
					write(d.Root+".install.complete.json", []byte("{"))
				}
			}
			before := runtimeSnapshot(t, root)
			if runDeploymentTransaction(v, i, f, uint32(os.Getuid())) == nil {
				t.Fatal("unsafe state accepted")
			}
			after := runtimeSnapshot(t, root)
			// Creating a private advisory lock is the only permitted refused-write.
			delete(before, "payload.install.lock")
			delete(after, "payload.install.lock")
			if string(mustJSON(before)) != string(mustJSON(after)) {
				t.Fatal("refusal changed payload")
			}
		})
	}
}

func TestDeploymentInspectionUnstartedCompleteAndRefusalsReadOnly(t *testing.T) {
	for _, kind := range []string{"unstarted", "complete", "foreign-intent", "partial-receipt", "foreign-lock", "changed-key", "extra-final-file", "unknown-unit"} {
		t.Run(kind, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			root := filepath.Dir(v.Deployment.Root)
			if kind != "unstarted" && kind != "foreign-lock" {
				if e := runDeploymentTransaction(v, i, f, uint32(os.Getuid())); e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "foreign-intent":
				os.WriteFile(v.Deployment.Root+".install.intent.json", []byte("foreign"), 0600)
			case "partial-receipt":
				os.WriteFile(v.Deployment.Root+".install.complete.json", []byte("{"), 0600)
			case "foreign-lock":
				os.Symlink("absent", v.Deployment.Root+".install.lock")
			case "changed-key":
				os.WriteFile(filepath.Join(v.Deployment.Root, "identity/identity.key"), []byte("other"), 0600)
			case "extra-final-file":
				os.WriteFile(filepath.Join(v.Deployment.Root, "unknown"), []byte("x"), 0600)
			case "unknown-unit":
				os.WriteFile(v.Deployment.Unit, []byte("x"), 0600)
			}
			before := runtimeSnapshot(t, root)
			state, e := inspectDeploymentTransaction(v, i, f, uint32(os.Getuid()))
			if kind == "unstarted" {
				if e != nil || state != "unstarted" {
					t.Fatal(state, e)
				}
			} else if kind == "complete" {
				if e != nil || state != "complete_private_payload" {
					t.Fatal(state, e)
				}
			} else if e == nil {
				t.Fatal("unknown state accepted", state)
			}
			if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, root))) {
				t.Fatal("inspection changed files")
			}
		})
	}
}
