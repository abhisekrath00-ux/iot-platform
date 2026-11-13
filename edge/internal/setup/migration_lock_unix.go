//go:build !windows

package setup

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func migrationLock(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, "migration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another secure pairing operation is running")
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
