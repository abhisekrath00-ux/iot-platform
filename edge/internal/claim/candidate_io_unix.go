//go:build !windows

package claim

import (
	"golang.org/x/sys/unix"
)

func candidateSyncDirectory(path string) error {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	return unix.Fsync(fd)
}

func candidateRead(dir, name string) ([]byte, error) {
	store, e := candidateOpenStore(dir, false)
	if e != nil {
		return nil, e
	}
	defer store.Close()
	return store.Read(name)
}
func candidateWrite(dir, name string, b []byte) error {
	store, e := candidateOpenStore(dir, false)
	if e != nil {
		return e
	}
	defer store.Close()
	return store.Write(name, b)
}
