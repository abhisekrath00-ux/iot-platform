//go:build windows

package setup

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func migrationLock(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, "migration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	o := &windows.Overlapped{}
	h := windows.Handle(f.Fd())
	if e = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, o); e != nil {
		f.Close()
		return nil, fmt.Errorf("another secure pairing operation is running")
	}
	return func() { windows.UnlockFileEx(h, 0, 1, 0, o); f.Close() }, nil
}
