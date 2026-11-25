//go:build !windows

package claim

import (
	"bytes"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestCandidatePinnedStoreSurvivesPathReplacement(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "attempt")
	store, e := candidateOpenStore(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if e = store.Write("identity.key", []byte("synthetic-original")); e != nil {
		t.Fatal(e)
	}
	moved := filepath.Join(parent, "retained")
	if e = os.Rename(dir, moved); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "identity.key"), []byte("substituted"), 0600); e != nil {
		t.Fatal(e)
	}
	out, e := store.Read("identity.key")
	if e != nil || !bytes.Equal(out, []byte("synthetic-original")) {
		t.Fatal("read redirected", e)
	}
	if e = store.Write("candidate-response.json", []byte("synthetic-response")); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "candidate-response.json")); !os.IsNotExist(e) {
		t.Fatal("write redirected", e)
	}
	out, e = os.ReadFile(filepath.Join(moved, "candidate-response.json"))
	if e != nil || string(out) != "synthetic-response" {
		t.Fatal(e)
	}
	// Now replace the pathname with a symlink. Pinned reads remain at the old
	// directory, while a new operation refuses rather than following the link.
	if e = os.RemoveAll(dir); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(moved, dir); e != nil {
		t.Fatal(e)
	}
	if out, e = store.Read("identity.key"); e != nil || string(out) != "synthetic-original" {
		t.Fatal(e)
	}
	if other, e := candidateOpenStore(dir, false); e == nil {
		other.Close()
		t.Fatal("symlink leaf followed")
	}
	// Replace the immediate parent as well. The open leaf descriptor remains
	// usable, and a new store must reject the symlink parent.
	parentMoved := parent + "-retained"
	if e = os.Rename(parent, parentMoved); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Remove(parent); _ = os.RemoveAll(parentMoved) })
	if e = os.Symlink(parentMoved, parent); e != nil {
		t.Fatal(e)
	}
	if out, e = store.Read("identity.key"); e != nil || string(out) != "synthetic-original" {
		t.Fatal("parent redirected read", e)
	}
	if other, e := candidateOpenStore(filepath.Join(parent, "retained"), false); e == nil {
		other.Close()
		t.Fatal("symlink parent followed")
	}
	// Remaining checks use the renamed real directory, not its old pathname.
	moved = filepath.Join(parentMoved, "retained")
	if _, e = store.Read("../identity.key"); e == nil {
		t.Fatal("path escape read")
	}
	if e = store.Write("../escape", []byte("x")); e == nil {
		t.Fatal("path escape write")
	}
	if e = unix.Mkfifo(filepath.Join(moved, "pipe"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Read("pipe"); e == nil {
		t.Fatal("FIFO accepted")
	}
	if e = os.Link(filepath.Join(moved, "identity.key"), filepath.Join(moved, "hard")); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Read("identity.key"); e == nil {
		t.Fatal("multi-link file accepted")
	}
	t.Log("PASS pinned directory read/write survive actual path replacement, reject fresh symlink traversal, escape, FIFO and hardlink")
}
