//go:build !windows

package claim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateNoFollowBoundedSingleLinkRead(t *testing.T) {
	d := t.TempDir()
	if e := os.Chmod(d, 0700); e != nil {
		t.Fatal(e)
	}
	if e := candidateWrite(d, "key", []byte("synthetic")); e != nil {
		t.Fatal(e)
	}
	if b, e := candidateRead(d, "key"); e != nil || string(b) != "synthetic" {
		t.Fatal(e)
	}
	if e := os.Link(filepath.Join(d, "key"), filepath.Join(d, "hard")); e != nil {
		t.Fatal(e)
	}
	if _, e := candidateRead(d, "key"); e == nil {
		t.Fatal("multiple link read")
	}
	_ = os.Remove(filepath.Join(d, "hard"))
	if e := os.Symlink(filepath.Join(d, "key"), filepath.Join(d, "sym")); e != nil {
		t.Fatal(e)
	}
	if _, e := candidateRead(d, "sym"); e == nil {
		t.Fatal("symlink read")
	}
	if _, e := candidateRead(d, "../key"); e == nil {
		t.Fatal("path traversal")
	}
	if e := candidateWrite(d, "large", make([]byte, 131073)); e != nil {
		t.Fatal(e)
	}
	if _, e := candidateRead(d, "large"); e == nil {
		t.Fatal("unbounded read")
	}
	if e := os.Chmod(filepath.Join(d, "key"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := candidateRead(d, "key"); e == nil {
		t.Fatal("permissive read")
	}
}
