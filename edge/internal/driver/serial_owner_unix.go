//go:build !windows

package driver

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func serialIdentity(name string) (string, string, error) {
	path, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return "", "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return "", "", fmt.Errorf("serial target must be a character device")
	}
	return fmt.Sprintf("unix-device:%d", st.Rdev), path, nil
}
func serialLease(path, key string) (func(), error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	fail := func(err error) (func(), error) { f.Close(); return nil, err }
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail(err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFCHR || fmt.Sprintf("unix-device:%d", st.Rdev) != key {
		return fail(fmt.Errorf("serial target changed while taking ownership"))
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(fmt.Errorf("port is busy in another cooperating process: %w", err))
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); f.Close() }, nil
}
