//go:build !windows

package claim

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// candidateStore pins each directory from the filesystem root with no-follow
// openat. Later rename/path replacement cannot redirect its reads or writes.
// It does not defend against a process already acting as the same user/root.
type candidateStore struct{ fd int }

func (s *candidateStore) Close() error { return unix.Close(s.fd) }
func candidateOpenStore(dir string, create bool) (*candidateStore, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(abs), string(filepath.Separator)), string(filepath.Separator))
	if len(parts) < 2 || parts[len(parts)-1] == "" {
		return nil, fmt.Errorf("private attempt leaf required")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	defer func() {
		if fd >= 0 {
			unix.Close(fd)
		}
	}()
	for idx, name := range parts {
		leaf := idx == len(parts)-1
		if leaf && create {
			if e = unix.Mkdirat(fd, name, 0700); e != nil {
				return nil, fmt.Errorf("attempt already present or unsafe; explicit recover only: %w", e)
			}
			if e = unix.Fsync(fd); e != nil {
				return nil, e
			}
		}
		next, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		var st unix.Stat_t
		e = unix.Fstat(next, &st)
		private := idx >= len(parts)-2
		owned := st.Uid == uint32(os.Getuid())
		safe := e == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR && (owned || st.Uid == 0)
		if private {
			safe = safe && owned && st.Mode&0077 == 0
		} else if st.Mode&0022 != 0 {
			safe = safe && (owned || st.Uid == 0) && st.Mode&unix.S_ISVTX != 0
		}
		if !safe {
			unix.Close(next)
			return nil, fmt.Errorf("unsafe pinned attempt ancestor")
		}
		unix.Close(fd)
		fd = next
	}
	out := &candidateStore{fd: fd}
	fd = -1
	return out, nil
}
func candidateExactName(name string) bool {
	return name != "" && name != "." && name != ".." && name == filepath.Base(name)
}
func (s *candidateStore) Read(name string) ([]byte, error) {
	if !candidateExactName(name) {
		return nil, fmt.Errorf("exact candidate filename required")
	}
	fd, e := unix.Openat(s.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 || st.Nlink != 1 || st.Size > 131072 {
		return nil, fmt.Errorf("private bounded single-link file required")
	}
	out, e := io.ReadAll(io.LimitReader(f, 131073))
	if e != nil {
		return nil, e
	}
	if len(out) > 131072 {
		return nil, fmt.Errorf("retained file grew past bound")
	}
	return out, nil
}
func (s *candidateStore) Write(name string, b []byte) error {
	if !candidateExactName(name) {
		return fmt.Errorf("exact candidate filename required")
	}
	fd, e := unix.Openat(s.fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), name)
	candidateFaultPartialResponse(f, name, b)
	n, e := f.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return unix.Fsync(s.fd)
}

func (s *candidateStore) MakePrivateDirectory(name string) error {
	if !candidateExactName(name) {
		return fmt.Errorf("exact private directory name required")
	}
	if e := unix.Mkdirat(s.fd, name, 0700); e != nil {
		return e
	}
	return unix.Fsync(s.fd)
}
