//go:build linux

package setup

import (
	"bytes"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func runtimeFixture(t *testing.T) (string, *runtimeBundleStore, runtimeBundleIntent, map[string][]byte) {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	fd, e := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		t.Fatal(e)
	}
	s := &runtimeBundleStore{fd}
	t.Cleanup(s.close)
	i, files := runtimeFixtureData()
	return p, s, i, files
}
func runtimeFixtureData() (runtimeBundleIntent, map[string][]byte) {
	files := map[string][]byte{"agent": []byte("synthetic nonexec executable payload"), "config.yaml": []byte("synthetic checked config"), "unit.service": []byte("synthetic inactive unit")}
	i := runtimeBundleIntent{Version: 1, Attempt: runtimeHash([]byte("attempt")), Identity: map[string]string{}, Files: map[string]string{}}
	for n, b := range files {
		i.Files[n] = runtimeHash(b)
	}
	for _, n := range []string{"identity.json", "identity.key", "identity.crt", "device-ca.pem"} {
		i.Identity[n] = runtimeHash([]byte(n))
	}
	return i, files
}
func TestRuntimeBundlePrivateCommitRepeatAndBindings(t *testing.T) {
	p, s, i, f := runtimeFixture(t)
	if e := commitRuntimeBundle(s, i, f); e != nil {
		t.Fatal(e)
	}
	for n, b := range f {
		a, e := os.ReadFile(filepath.Join(p, "runtime", n))
		if e != nil || !bytes.Equal(a, b) {
			t.Fatal(n, e)
		}
		st, e := os.Stat(filepath.Join(p, "runtime", n))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("nonexec private file", e)
		}
	}
	if e := commitRuntimeBundle(s, i, f); e != nil {
		t.Fatal("exact repeat", e)
	}
	i.Identity["identity.key"] = runtimeHash([]byte("other-key"))
	if e := commitRuntimeBundle(s, i, f); e == nil {
		t.Fatal("identity binding changed")
	}
	b, e := os.ReadFile(filepath.Join(p, "runtime", "agent"))
	if e != nil || !bytes.Equal(b, f["agent"]) {
		t.Fatal("changed installed payload")
	}
}
func TestRuntimeBundleRefusesUnboundPartialAndUnexpected(t *testing.T) {
	for _, state := range []string{"unbound-destination", "unbound-stage", "unbound-receipt", "partial-intent", "partial-receipt", "partial-file", "gap", "extra", "symlink", "hardlink", "executable", "root-mode"} {
		t.Run(state, func(t *testing.T) {
			p, s, i, f := runtimeFixture(t)
			raw, _ := json.Marshal(i)
			write := func(n string, b []byte) {
				t.Helper()
				if e := os.WriteFile(filepath.Join(p, n), b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			mkdir := func(n string) {
				t.Helper()
				if e := os.Mkdir(filepath.Join(p, n), 0700); e != nil {
					t.Fatal(e)
				}
			}
			switch state {
			case "unbound-destination":
				mkdir("runtime")
			case "unbound-stage":
				mkdir("runtime.pending")
			case "unbound-receipt":
				write("runtime.complete.json", raw)
			case "partial-intent":
				write("runtime.intent.json", raw[:len(raw)/2])
			case "root-mode":
				os.Chmod(p, 0755)
			default:
				write("runtime.intent.json", raw)
				mkdir("runtime.pending")
				switch state {
				case "partial-receipt":
					os.Rename(filepath.Join(p, "runtime.pending"), filepath.Join(p, "runtime"))
					for n, b := range f {
						write("runtime/"+n, b)
					}
					write("runtime.complete.json", raw[:len(raw)/2])
				case "partial-file":
					write("runtime.pending/agent", []byte("partial"))
				case "gap":
					write("runtime.pending/config.yaml", f["config.yaml"])
				case "extra":
					write("runtime.pending/extra", []byte("unexpected"))
				case "symlink":
					write("outside", f["agent"])
					os.Symlink(filepath.Join(p, "outside"), filepath.Join(p, "runtime.pending/agent"))
				case "hardlink":
					write("outside", f["agent"])
					os.Link(filepath.Join(p, "outside"), filepath.Join(p, "runtime.pending/agent"))
				case "executable":
					write("runtime.pending/agent", f["agent"])
					os.Chmod(filepath.Join(p, "runtime.pending/agent"), 0700)
				}
			}
			before := runtimeSnapshot(t, p)
			if e := commitRuntimeBundle(s, i, f); e == nil {
				t.Fatal("unsafe state accepted")
			}
			after := runtimeSnapshot(t, p)
			delete(before, "runtime.lock")
			delete(after, "runtime.lock")
			if !bytes.Equal(mustJSON(before), mustJSON(after)) {
				t.Fatal("refusal changed state")
			}
		})
	}
}
func runtimeSnapshot(t *testing.T, p string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.Walk(p, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if path == p {
			return nil
		}
		n, _ := filepath.Rel(p, path)
		if info.Mode()&os.ModeSymlink != 0 {
			v, e := os.Readlink(path)
			if e != nil {
				return e
			}
			out[n] = "link:" + v
		} else if info.IsDir() {
			out[n] = "dir"
		} else {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			out[n] = runtimeHash(b)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func TestRuntimeBundleCooperatingLock(t *testing.T) {
	p, s, i, f := runtimeFixture(t)
	fd, e := unix.Open(filepath.Join(p, "runtime.lock"), unix.O_CREAT|unix.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(fd)
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	if e = commitRuntimeBundle(s, i, f); e == nil {
		t.Fatal("busy transaction accepted")
	}
	if _, e = os.Stat(filepath.Join(p, "runtime.intent.json")); !os.IsNotExist(e) {
		t.Fatal("busy transaction wrote intent")
	}
}
