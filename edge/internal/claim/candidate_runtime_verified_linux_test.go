//go:build linux

package claim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedRuntimeStateHandleRefusesReplacedPath(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "state")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	held, e := candidateOpenRuntimeState(path)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	if e = candidateCheckRuntimeState(held, path); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(path, path+"-original"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e = candidateCheckRuntimeState(held, path); e == nil {
		t.Fatal("replacement state accepted")
	}
	a, e := held.Stat()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.Stat(path + "-original")
	if e != nil || !os.SameFile(a, b) {
		t.Fatal("held state lost original identity", e)
	}
}
func TestVerifiedRuntimeStateRefusesSymlinkAndNonprivate(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "state")
	if e := os.Mkdir(path, 0755); e != nil {
		t.Fatal(e)
	}
	if f, e := candidateOpenRuntimeState(path); e == nil {
		f.Close()
		t.Fatal("nonprivate state accepted")
	}
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(path, path+"-link"); e != nil {
		t.Fatal(e)
	}
	if f, e := candidateOpenRuntimeState(path + "-link"); e == nil {
		f.Close()
		t.Fatal("symlink state accepted")
	}
}
