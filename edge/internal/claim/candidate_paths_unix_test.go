//go:build !windows

package claim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCandidatePrivatePaths(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "attempt")
	if e := candidatePrivateParent(dir); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "key"), []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := candidatePrivateLeaf(dir, "key"); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(dir, "key"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := candidatePrivateLeaf(dir, "key"); e == nil {
		t.Fatal("readable key")
	}
	if e := os.Chmod(parent, 0777); e != nil {
		t.Fatal(e)
	}
	if e := candidatePrivateParent(dir); e == nil {
		t.Fatal("writable parent")
	}
	_ = os.Chmod(parent, 0700)
	link := filepath.Join(parent, "alias")
	if e := os.Symlink(dir, link); e != nil {
		t.Fatal(e)
	}
	if e := candidatePrivateLeaf(link, "key"); e == nil {
		t.Fatal("symlink leaf")
	}
}
