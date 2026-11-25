//go:build linux

package setup

// Private runtime payload commit primitive. Not a system installer. It never
// executes the payload, modifies existing targets or changes identity ownership.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"golang.org/x/sys/unix"
)

type runtimeBundleIntent struct {
	Version    int
	Attempt    string
	Identity   map[string]string
	Files      map[string]string
	Deployment *claim.VerifiedDeployment `json:",omitempty"`
}

type runtimeBundleStore struct{ fd int }

func runtimeBundleOpenAt(parent int, name string) (*runtimeBundleStore, error) {
	fd, e := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	e = unix.Fstat(fd, &st)
	if e != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0777 != 0700 {
		unix.Close(fd)
		return nil, fmt.Errorf("private owned runtime payload directory required")
	}
	return &runtimeBundleStore{fd}, nil
}
func (s *runtimeBundleStore) close() { unix.Close(s.fd) }
func (s *runtimeBundleStore) read(name string, limit int64) ([]byte, error) {
	fd, e := unix.Openat(s.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0777 != 0600 || st.Nlink != 1 || st.Size > limit {
		return nil, fmt.Errorf("private bounded single-link runtime file required")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("bounded runtime file required")
	}
	return b, nil
}
func (s *runtimeBundleStore) write(name string, b []byte) error {
	fd, e := unix.Openat(s.fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), name)
	runtimeBundlePartial(f, name, b)
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
func runtimeHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func runtimeValidHash(h string) bool {
	b, e := hex.DecodeString(h)
	return e == nil && len(b) == 32 && h == hex.EncodeToString(b)
}
func runtimeBundleNames(s *runtimeBundleStore) ([]string, error) {
	fd, e := unix.Openat(s.fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "runtime-bundle")
	defer f.Close()
	return f.Readdirnames(-1)
}

// commitRuntimeBundle requires its caller to have freshly verified the exact
// retained attempt, identity and mapping. Digests establish equality only, not
// current authorization. Root is an already pinned private directory owned by
// this process. Only cooperating processes are excluded by the advisory lock.
func commitRuntimeBundle(root *runtimeBundleStore, intent runtimeBundleIntent, files map[string][]byte) error {
	return commitNamedRuntimeBundle(root, intent, files, "runtime")
}

func commitNamedRuntimeBundle(root *runtimeBundleStore, intent runtimeBundleIntent, files map[string][]byte, bundle string) error {
	order := []string{"agent", "config.yaml", "unit.service"}
	if bundle == "handoff" {
		if intent.Version != 2 || intent.Deployment == nil || intent.Deployment.Activation != "stopped-only" {
			return fmt.Errorf("verified stopped handoff required")
		}
		d := intent.Deployment
		if !validNumericServiceID(d.ServiceUID) || !validNumericServiceID(d.ServiceGID) || len(d.ParentSnapshot) != 3 || d.Root == "" || d.State == "" || d.Unit == "" {
			return fmt.Errorf("complete deployment contract required")
		}
		for _, n := range []string{"deployment_parent", "deployment_state_parent", "deployment_unit_parent"} {
			if d.ParentSnapshot[n] == "" {
				return fmt.Errorf("complete deployment parent binding required")
			}
		}
		for n, h := range intent.Identity {
			if runtimeHash(files[n]) != h {
				return fmt.Errorf("handoff identity differs from accepted identity")
			}
		}
		order = append(order, "broker-ca.pem", "device-ca.pem", "identity.json", "identity.crt", "identity.key")
	} else if bundle != "runtime" || intent.Version != 1 || intent.Deployment != nil {
		return fmt.Errorf("exact private bundle kind required")
	}
	var rootStat unix.Stat_t
	if root == nil || unix.Fstat(root.fd, &rootStat) != nil || rootStat.Mode&unix.S_IFMT != unix.S_IFDIR || rootStat.Uid != uint32(os.Getuid()) || rootStat.Mode&0777 != 0700 {
		return fmt.Errorf("pinned private runtime root required")
	}
	if !runtimeValidHash(intent.Attempt) || len(intent.Identity) != 4 || len(files) != len(order) || len(intent.Files) != len(order) {
		return fmt.Errorf("complete bound runtime payload required")
	}
	for _, name := range []string{"identity.json", "identity.key", "identity.crt", "device-ca.pem"} {
		if !runtimeValidHash(intent.Identity[name]) {
			return fmt.Errorf("complete identity fingerprint binding required")
		}
	}
	for _, name := range order {
		b, ok := files[name]
		limit := 2 << 20
		if name == "agent" {
			limit = 64 << 20
		}
		if !ok || len(b) == 0 || len(b) > limit || !runtimeValidHash(intent.Files[name]) || runtimeHash(b) != intent.Files[name] {
			return fmt.Errorf("runtime payload digest differs")
		}
	}
	fd, e := unix.Openat(root.fd, bundle+".lock", unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 || st.Mode&0077 != 0 {
		return fmt.Errorf("private runtime lock required")
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fmt.Errorf("runtime transaction busy")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	raw, e := json.Marshal(intent)
	if e != nil {
		return e
	}
	old, e := root.read(bundle+".intent.json", 131072)
	if os.IsNotExist(e) {
		for _, name := range []string{bundle + ".pending", bundle, bundle + ".complete.json"} {
			var v unix.Stat_t
			if e = unix.Fstatat(root.fd, name, &v, unix.AT_SYMLINK_NOFOLLOW); e != unix.ENOENT {
				return fmt.Errorf("unbound runtime destination exists")
			}
		}
		if e = root.write(bundle+".intent.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint(bundle + "_after_intent")
	} else if e != nil || !bytes.Equal(old, raw) {
		return fmt.Errorf("runtime intent partial or differs; preserve it")
	}
	// Refuse impossible receipt/stage/destination combinations before filling.
	var dst, stage, receipt unix.Stat_t
	de := unix.Fstatat(root.fd, bundle, &dst, unix.AT_SYMLINK_NOFOLLOW)
	se := unix.Fstatat(root.fd, bundle+".pending", &stage, unix.AT_SYMLINK_NOFOLLOW)
	re := unix.Fstatat(root.fd, bundle+".complete.json", &receipt, unix.AT_SYMLINK_NOFOLLOW)
	if de != nil && de != unix.ENOENT || se != nil && se != unix.ENOENT || re != nil && re != unix.ENOENT {
		return fmt.Errorf("runtime state unavailable")
	}
	if de == nil && se == nil || re == nil && de != nil {
		return fmt.Errorf("inconsistent runtime state; preserve it")
	}
	if re == nil {
		saved, e := root.read(bundle+".complete.json", 131072)
		if e != nil || !bytes.Equal(saved, raw) {
			return fmt.Errorf("runtime receipt partial or differs")
		}
	}
	if de == unix.ENOENT {
		if se == unix.ENOENT {
			if e = unix.Mkdirat(root.fd, bundle+".pending", 0700); e != nil {
				return e
			}
			if e = unix.Fsync(root.fd); e != nil {
				return e
			}
			runtimeBundleCheckpoint(bundle + "_after_directory")
		}
		pending, e := runtimeBundleOpenAt(root.fd, bundle+".pending")
		if e != nil {
			return e
		}
		defer pending.close()
		names, e := runtimeBundleNames(pending)
		if e != nil {
			return e
		}
		sort.Strings(names)
		present := map[string]bool{}
		for _, name := range names {
			b, ok := files[name]
			if !ok {
				return fmt.Errorf("unexpected runtime stage entry")
			}
			old, e := pending.read(name, int64(len(b)))
			if e != nil || !bytes.Equal(old, b) {
				return fmt.Errorf("partial runtime stage differs; preserve prefix")
			}
			present[name] = true
		}
		gap := false
		for _, name := range order {
			if !present[name] {
				gap = true
			} else if gap {
				return fmt.Errorf("runtime stage is not an exact complete-file prefix")
			}
		}
		for _, name := range order {
			if !present[name] {
				if e = pending.write(name, files[name]); e != nil {
					return e
				}
				runtimeBundleCheckpoint(bundle + "_after_" + name)
			}
		}
		if e = verifyRuntimeBundle(pending, files); e != nil {
			return e
		}
		if e = unix.Renameat2(root.fd, bundle+".pending", root.fd, bundle, unix.RENAME_NOREPLACE); e != nil {
			return e
		}
		if e = unix.Fsync(root.fd); e != nil {
			return e
		}
		runtimeBundleCheckpoint(bundle + "_after_promotion")
	}
	installed, e := runtimeBundleOpenAt(root.fd, bundle)
	if e != nil {
		return e
	}
	defer installed.close()
	if e = verifyRuntimeBundle(installed, files); e != nil {
		return e
	}
	if re == unix.ENOENT {
		if e = root.write(bundle+".complete.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint(bundle + "_after_receipt")
	}
	return nil
}
func verifyRuntimeBundle(s *runtimeBundleStore, files map[string][]byte) error {
	names, e := runtimeBundleNames(s)
	if e != nil || len(names) != len(files) {
		return fmt.Errorf("runtime payload pathset differs")
	}
	for _, name := range names {
		want, ok := files[name]
		if !ok {
			return fmt.Errorf("unexpected runtime payload entry")
		}
		b, e := s.read(name, int64(len(want)))
		if e != nil || !bytes.Equal(b, want) {
			return fmt.Errorf("runtime payload differs")
		}
	}
	return nil
}
