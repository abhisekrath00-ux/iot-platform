//go:build !windows

package claim

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Candidate guard only. Reject symlink ancestors and untrusted writable parents.
// Root/self-owned sticky shared ancestors such as /tmp are allowed only above the
// immediate parent, which must be owner-only and owned by this process user.
func candidatePrivateParent(dir string) error {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return e
	}
	parent := filepath.Dir(abs)
	for p := parent; ; p = filepath.Dir(p) {
		fi, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe attempt ancestor")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("owner unverifiable")
		}
		uid := int(st.Uid)
		if p == parent {
			if uid != os.Getuid() || fi.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("private owned parent required")
			}
		} else if fi.Mode().Perm()&0022 != 0 && !((uid == 0 || uid == os.Getuid()) && fi.Mode()&os.ModeSticky != 0) {
			return fmt.Errorf("writable ancestor refused")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func candidatePrivateLeaf(dir string, names ...string) error {
	if e := candidatePrivateParent(dir); e != nil {
		return e
	}
	for _, n := range append([]string{""}, names...) {
		p := dir
		if n != "" {
			p = filepath.Join(dir, n)
		}
		fi, e := os.Lstat(p)
		if e != nil {
			return e
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != os.Getuid() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0077 != 0 || (n == "" && !fi.IsDir()) || (n != "" && !fi.Mode().IsRegular()) {
			return fmt.Errorf("private retained attempt path refused")
		}
	}
	return nil
}
