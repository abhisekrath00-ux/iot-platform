//go:build linux

package claim

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func candidateReadPreparedBinary(s *candidateStore) ([]byte, error) {
	fd, e := unix.Openat(s.fd, "prepared-agent", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "prepared-agent")
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0777 != 0600 || st.Nlink != 1 || st.Size > 64<<20 {
		return nil, fmt.Errorf("private prepared executable required")
	}
	b, e := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if e != nil || len(b) > 64<<20 {
		return nil, fmt.Errorf("bounded prepared executable required")
	}
	return b, nil
}
func candidateVerifyAcceptedRuntime(parent *candidateStore, leaf, attempt string, files map[string][]byte) error {
	if e := candidateVerifyDestination(parent, leaf, files); e != nil {
		return e
	}
	hashes := map[string]string{}
	for n, b := range files {
		hashes[n] = func() string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }()
	}
	raw, _ := json.Marshal(candidateDestinationIntent{1, attempt, leaf, hashes})
	for _, name := range []string{leaf + ".intent.json", leaf + ".complete.json"} {
		old, e := parent.Read(name)
		if e != nil || !bytes.Equal(old, raw) {
			return fmt.Errorf("accepted identity receipt differs")
		}
	}
	return nil
}

func candidateOpenRuntimeState(path string) (*os.File, error) {
	fd, e := candidateOpenTargetDirectory(path)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0777 != 0700 {
		unix.Close(fd)
		return nil, fmt.Errorf("prebound private runtime state required")
	}
	return os.NewFile(uintptr(fd), "prebound-runtime-state"), nil
}
func candidateCheckRuntimeState(held *os.File, path string) error {
	current, e := candidateOpenRuntimeState(path)
	if e != nil {
		return e
	}
	defer current.Close()
	a, e := held.Stat()
	if e != nil {
		return e
	}
	b, e := current.Stat()
	if e != nil || !os.SameFile(a, b) {
		return fmt.Errorf("prebound runtime state replaced")
	}
	return nil
}
