package applyadmission

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFenceConcurrentAndRestart(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := OpenFence(dir)
			if err != nil {
				t.Error(err)
				return
			}
			if f.Claim("tenant", "gateway", "attempt", "digest") == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal(winners.Load())
	}
	f, err := OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Claim("tenant", "gateway", "new", "digest") == nil {
		t.Fatal("restart rearmed")
	}
	if f.Claim("tenant", "other", "new", "digest") != nil {
		t.Fatal("unrelated gateway blocked")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, _ := e.Info()
		if info.Mode().Perm() != 0600 {
			t.Fatal("shared intent")
		}
	}
}
func TestFenceRefusesMissingRedirectedOrPartial(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f, err := OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Claim("tenant", "gateway", "attempt", "digest") != nil {
		t.Fatal("initial claim")
	}
	entries, _ := os.ReadDir(dir)
	os.WriteFile(filepath.Join(dir, entries[0].Name()), nil, 0600)
	if f.Claim("tenant", "gateway", "new", "digest") == nil {
		t.Fatal("partial intent rearmed")
	}
	os.RemoveAll(dir)
	if f.Claim("tenant", "other", "new", "digest") == nil {
		t.Fatal("missing directory rearmed")
	}
	target := t.TempDir()
	os.Chmod(target, 0700)
	os.Symlink(target, dir)
	if _, err := OpenFence(dir); err == nil {
		t.Fatal("redirected journal accepted")
	}
	if (*Fence)(nil).Claim("a", "b", "c", "d") == nil {
		t.Fatal("missing fence admitted")
	}
}

func TestFenceProcessExitBoundary(t *testing.T) {
	if dir := os.Getenv("TEST_APPLY_FENCE_CHILD"); dir != "" {
		f, err := OpenFence(dir)
		if err != nil {
			os.Exit(10)
		}
		if f.Claim("tenant", "gateway", "attempt", "digest") != nil {
			os.Exit(11)
		}
		// Exit without a normal return to the parent workflow.
		os.Exit(0)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFenceProcessExitBoundary$")
	cmd.Env = append(os.Environ(), "TEST_APPLY_FENCE_CHILD="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	f, err := OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Claim("tenant", "gateway", "new", "digest") == nil {
		t.Fatal("process exit rearmed")
	}
}

func TestFenceReadOnlyStatus(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if occupied, err := f.Occupied("tenant", "gateway"); err != nil || occupied {
		t.Fatal(occupied, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("status wrote to storage", entries, err)
	}
	if err := f.Claim("tenant", "gateway", "attempt", "digest"); err != nil {
		t.Fatal(err)
	}
	if occupied, err := f.Occupied("tenant", "gateway"); err != nil || !occupied {
		t.Fatal(occupied, err)
	}
	entries, _ = os.ReadDir(dir)
	if err := os.WriteFile(filepath.Join(dir, entries[0].Name()), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if occupied, err := f.Occupied("tenant", "gateway"); err != nil || !occupied {
		t.Fatal("partial record hidden", occupied, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Occupied("tenant", "gateway"); err == nil {
		t.Fatal("missing storage treated as empty")
	}
	if _, err := (*Fence)(nil).Occupied("a", "b"); err == nil {
		t.Fatal("nil storage treated as empty")
	}
}

func TestFenceImmutableProgressionAndStaleHead(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f, err := OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Claim("t", "g", "first", "digest1"); err != nil {
		t.Fatal(err)
	}
	first, err := f.Latest("t", "g")
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.ClaimNext("t", "g", "second", "digest2", 2, first) == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal(winners.Load())
	}
	if err := f.ClaimNext("t", "g", "stale", "digest1", 2, first); err == nil {
		t.Fatal("stale head rearmed")
	}
	latest, err := f.Latest("t", "g")
	if err != nil || latest.ID != "second" || latest.Previous != "first" {
		t.Fatal(latest, err)
	}
	if err := os.Remove(f.attemptPath("t", "g", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Latest("t", "g"); err == nil {
		t.Fatal("deleted predecessor hidden")
	}
	if err := f.Claim("t", "g", "rearmed", "digest"); err == nil {
		t.Fatal("gapped chain rearmed")
	}
}

func TestBoundFenceIdentityAndReplacement(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	if _, err := OpenBoundFence(dir, ""); err == nil {
		t.Fatal("missing independent ID accepted")
	}
	id, err := ProvisionFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProvisionFence(dir); err == nil {
		t.Fatal("existing store reinitialized")
	}
	f, err := OpenBoundFence(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.RequireBound(); err != nil {
		t.Fatal(err)
	}
	if err := f.Claim("t", "g", "first", "digest"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenBoundFence(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Claim("t", "g", "retry", "digest"); err == nil {
		t.Fatal("reopen rearmed")
	}
	old := dir + "-retained"
	if err := os.Rename(dir, old); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(old)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.Claim("t", "other", "new", "digest"); err == nil {
		t.Fatal("empty replacement rearmed")
	}
	if _, err := OpenBoundFence(dir, id); err == nil {
		t.Fatal("missing marker reopened")
	}
	otherID, err := ProvisionFence(dir)
	if err != nil || otherID == id {
		t.Fatal(err)
	}
	if _, err := OpenBoundFence(dir, id); err == nil {
		t.Fatal("different newly provisioned store accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("replacement acquired intent", entries, err)
	}
	if _, err := OpenBoundFence(old, id); err != nil {
		t.Fatal("retained original cannot reopen at moved path", err)
	}
}

func TestFencePinnedRootDoesNotFollowReplacement(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	f, err := OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := f.root("t", "g")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	old := dir + "-old"
	if err := os.Rename(dir, old); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(old)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := root.OpenFile("pinned-fixture", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := os.Stat(filepath.Join(dir, "pinned-fixture")); !os.IsNotExist(err) {
		t.Fatal("root redirected", err)
	}
	if _, err := os.Stat(filepath.Join(old, "pinned-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := f.check("t", "g"); err == nil {
		t.Fatal("replaced directory not refused")
	}
}
