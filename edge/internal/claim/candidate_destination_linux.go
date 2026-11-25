//go:build linux

package claim

// Dormant destination primitive. Caller must hold verified retained material
// and current server authorization. Not exposed by product commands yet.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"golang.org/x/sys/unix"
)

type candidateDestinationIntent struct {
	Version              int
	Attempt, Destination string
	Files                map[string]string
}

func candidateAcceptDestination(parent, leaf, attempt string, files map[string][]byte) error {
	if !candidateExactName(leaf) || len(leaf) > 80 || len(attempt) != 64 || len(files) != 4 {
		return fmt.Errorf("exact destination and identity set required")
	}
	if _, e := hex.DecodeString(attempt); e != nil {
		return e
	}
	required := []string{"identity.json", "identity.key", "identity.crt", "device-ca.pem"}
	hashes := map[string]string{}
	for _, name := range required {
		b, ok := files[name]
		if !ok || len(b) == 0 || len(b) > 131072 {
			return fmt.Errorf("bounded complete identity set required")
		}
		h := sha256.Sum256(b)
		hashes[name] = hex.EncodeToString(h[:])
	}
	root, e := candidateOpenStore(parent, false)
	if e != nil {
		return e
	}
	defer root.Close()
	// Single local transaction worker. Advisory lock protects cooperating product
	// processes only; it is not protection against same-user/root interference.
	lockfd, e := unix.Openat(root.fd, leaf+".lock", unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	defer unix.Close(lockfd)
	var st unix.Stat_t
	if e = unix.Fstat(lockfd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 {
		return fmt.Errorf("unsafe transaction lock")
	}
	if e = unix.Flock(lockfd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fmt.Errorf("destination transaction busy")
	}
	defer unix.Flock(lockfd, unix.LOCK_UN)
	intent := candidateDestinationIntent{1, attempt, leaf, hashes}
	raw, _ := json.Marshal(intent)
	journal := leaf + ".intent.json"
	stage := leaf + ".pending"
	receipt := leaf + ".complete.json"
	old, e := root.Read(journal)
	if os.IsNotExist(e) {
		// Refuse any destination/stage/receipt predating our exact durable intent.
		for _, name := range []string{leaf, stage, receipt} {
			var v unix.Stat_t
			if e = unix.Fstatat(root.fd, name, &v, unix.AT_SYMLINK_NOFOLLOW); e == nil || e != unix.ENOENT {
				return fmt.Errorf("unbound destination state exists")
			}
		}
		if e = root.Write(journal, raw); e != nil {
			return e
		}
		candidateFaultCheckpoint("destination_after_intent")
	} else if e != nil || !bytes.Equal(old, raw) {
		return fmt.Errorf("destination intent partial or differs; preserve all state")
	}
	// Exact durable intent plus freshly verified source authorizes filling only
	// absent names. Existing partial/mismatched files are never overwritten.
	var destinationStat, stageStat unix.Stat_t
	de0 := unix.Fstatat(root.fd, leaf, &destinationStat, unix.AT_SYMLINK_NOFOLLOW)
	se0 := unix.Fstatat(root.fd, stage, &stageStat, unix.AT_SYMLINK_NOFOLLOW)
	if de0 == unix.ENOENT {
		if se0 == unix.ENOENT {
			if e = root.MakePrivateDirectory(stage); e != nil {
				return e
			}
			candidateFaultCheckpoint("destination_after_directory")
		} else if se0 != nil {
			return se0
		}
		fd, e := unix.Openat(root.fd, stage, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		staged := &candidateStore{fd: fd}
		if e = candidateValidateDestinationPrefix(staged, files); e != nil {
			staged.Close()
			return e
		}
		for _, name := range required {
			saved, readErr := staged.Read(name)
			if os.IsNotExist(readErr) {
				if e = staged.Write(name, files[name]); e != nil {
					staged.Close()
					return e
				}
				candidateFaultCheckpoint("destination_after_" + name)
			} else if readErr != nil || !bytes.Equal(saved, files[name]) {
				staged.Close()
				return fmt.Errorf("partial identity file differs; preserve prefix")
			}
		}
		staged.Close()
	}
	// Reconciliation never overwrites an existing file or partial byte prefix.
	// A complete private staged set can be promoted; an exact promoted set can
	// finish its receipt. Every other combination stays blocked.
	var dst, src unix.Stat_t
	de := unix.Fstatat(root.fd, leaf, &dst, unix.AT_SYMLINK_NOFOLLOW)
	se := unix.Fstatat(root.fd, stage, &src, unix.AT_SYMLINK_NOFOLLOW)
	if de == unix.ENOENT && se == nil {
		if e = candidateVerifyDestination(root, stage, files); e != nil {
			return e
		}
		candidateFaultCheckpoint("destination_before_promotion")
		if e = unix.Renameat2(root.fd, stage, root.fd, leaf, unix.RENAME_NOREPLACE); e != nil {
			return e
		}
		if e = unix.Fsync(root.fd); e != nil {
			return e
		}
		candidateFaultCheckpoint("destination_after_promotion")
	} else if de != nil || se != unix.ENOENT {
		return fmt.Errorf("destination state ambiguous; preserve all state")
	}
	if e = candidateVerifyDestination(root, leaf, files); e != nil {
		return e
	}
	saved, e := root.Read(receipt)
	if os.IsNotExist(e) {
		if e = root.Write(receipt, raw); e != nil {
			return e
		}
		candidateFaultCheckpoint("destination_after_receipt")
	} else if e != nil || !bytes.Equal(saved, raw) {
		return fmt.Errorf("completion receipt differs or is partial")
	}
	return nil
}

func candidateVerifyDestination(root *candidateStore, name string, files map[string][]byte) error {
	fd, e := unix.Openat(root.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	store := &candidateStore{fd: fd}
	defer store.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 {
		return fmt.Errorf("unsafe identity destination directory")
	}
	copyfd, e := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	dir := os.NewFile(uintptr(copyfd), filepath.Base(name))
	entries, e := dir.ReadDir(-1)
	dir.Close()
	if e != nil {
		return e
	}
	got := []string{}
	want := []string{}
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	for n := range files {
		want = append(want, n)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("identity destination pathset differs")
	}
	for n, want := range files {
		got, e := store.Read(n)
		if e != nil || !bytes.Equal(got, want) {
			return fmt.Errorf("identity destination partial or differs")
		}
	}
	return nil
}

func candidateValidateDestinationPrefix(store *candidateStore, files map[string][]byte) error {
	var st unix.Stat_t
	if e := unix.Fstat(store.fd, &st); e != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 {
		return fmt.Errorf("unsafe pending identity directory")
	}
	fd, e := unix.Openat(store.fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), "pending")
	entries, e := f.ReadDir(-1)
	f.Close()
	if e != nil {
		return e
	}
	for _, entry := range entries {
		want, ok := files[entry.Name()]
		if !ok {
			return fmt.Errorf("unknown pending identity file")
		}
		got, e := store.Read(entry.Name())
		if e != nil || !bytes.Equal(got, want) {
			return fmt.Errorf("partial pending identity file differs")
		}
	}
	return nil
}
