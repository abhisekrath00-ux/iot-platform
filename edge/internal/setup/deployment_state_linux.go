//go:build linux

package setup

// Dormant independently bound writable-state transition. Frozen payload and
// identity never become service-writable. No service/unit/systemd effects.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func (t *deploymentTransaction) stateIntent() []byte {
	raw, _ := json.Marshal(struct {
		Version int
		Phase   string
		Payload runtimeBundleIntent
	}{1, "empty_writable_state", t.intent})
	return raw
}
func (t *deploymentTransaction) inspectWritableState(serviceUID, serviceGID uint32) (string, error) {
	return t.inspectWritableStateMode(serviceUID, serviceGID, false)
}
func (t *deploymentTransaction) inspectWritableStateMode(serviceUID, serviceGID uint32, allowRuntimeData bool) (string, error) {
	p := t.parents["deployment_state_parent"]
	leaf := t.leaves["deployment_state_parent"]
	raw := t.stateIntent()
	old, e := p.read(leaf+".state.intent.json", 131072)
	pending := !p.absent(leaf + ".state.pending")
	final := !p.absent(leaf)
	binding := !p.absent(leaf + ".state.inode.json")
	receipt := !p.absent(leaf + ".state.complete.json")
	if os.IsNotExist(e) {
		if pending || final || binding || receipt {
			return "", fmt.Errorf("unbound writable state")
		}
		return "state_unstarted", nil
	}
	if e != nil || !bytes.Equal(old, raw) || pending && final || receipt && !final {
		return "", fmt.Errorf("partial or inconsistent state intent")
	}
	if !pending && !final {
		if binding || receipt {
			return "", fmt.Errorf("state inode absent")
		}
		return "state_intent_only", nil
	}
	name := leaf + ".state.pending"
	if final {
		name = leaf
	}
	fd, e := unix.Openat(p.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return "", e
	}
	s := &deploymentStore{fd, p.uid}
	defer s.close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&07777 != 0700 {
		return "", fmt.Errorf("private writable state directory required")
	}
	names, e := s.names()
	if e != nil || !allowRuntimeData && len(names) != 0 {
		return "", fmt.Errorf("unexpected state contents before activation")
	}
	owned := st.Uid == serviceUID && st.Gid == serviceGID
	original := st.Uid == p.uid && st.Gid == uint32(os.Getgid())
	if !owned && !original {
		return "", fmt.Errorf("unknown writable state ownership")
	}
	inode := []byte(fmt.Sprintf("%d:%d", st.Dev, st.Ino))
	b, e := p.read(leaf+".state.inode.json", 131072)
	if os.IsNotExist(e) {
		if final || receipt || !original {
			return "", fmt.Errorf("unbound handed-off state inode")
		}
		return "state_empty_unbound_inode", nil
	}
	if e != nil || !bytes.Equal(b, inode) {
		return "", fmt.Errorf("state inode partial or replaced")
	}
	if final && !owned {
		return "", fmt.Errorf("state promoted before exact ownership")
	}
	if receipt {
		b, e = p.read(leaf+".state.complete.json", 131072)
		expected := append(append([]byte{}, raw...), inode...)
		if e != nil || !bytes.Equal(b, expected) {
			return "", fmt.Errorf("state receipt partial or differs")
		}
		return "state_complete_empty", nil
	}
	if final {
		return "state_promoted_no_receipt", nil
	}
	if owned {
		return "state_owned_pending", nil
	}
	return "state_bound_private", nil
}
func (t *deploymentTransaction) commitWritableState(uid, serviceUID, serviceGID uint32) error {
	if serviceUID == 0 || serviceGID == 0 {
		return fmt.Errorf("nonroot writable state owner required")
	}
	if e := t.validateParents(uid); e != nil {
		return e
	}
	p := t.parents["deployment_state_parent"]
	leaf := t.leaves["deployment_state_parent"]
	state, e := t.inspectWritableState(serviceUID, serviceGID)
	if e != nil {
		return e
	}
	if state == "state_unstarted" {
		if e = p.write(leaf+".state.intent.json", t.stateIntent()); e != nil {
			return e
		}
		runtimeBundleCheckpoint("state_after_intent")
	}
	if p.absent(leaf) && p.absent(leaf+".state.pending") {
		if e = unix.Mkdirat(p.fd, leaf+".state.pending", 0700); e != nil {
			return e
		}
		if e = unix.Fsync(p.fd); e != nil {
			return e
		}
		runtimeBundleCheckpoint("state_after_directory")
	}
	name := leaf + ".state.pending"
	if p.absent(name) {
		name = leaf
	}
	fd, e := unix.Openat(p.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		return fmt.Errorf("state inode unavailable")
	}
	inode := []byte(fmt.Sprintf("%d:%d", st.Dev, st.Ino))
	if p.absent(leaf + ".state.inode.json") {
		if e = p.write(leaf+".state.inode.json", inode); e != nil {
			return e
		}
		runtimeBundleCheckpoint("state_after_inode")
	}
	if st.Uid != serviceUID || st.Gid != serviceGID {
		if e = unix.Fchown(fd, int(serviceUID), int(serviceGID)); e != nil {
			return e
		}
		if e = unix.Fsync(fd); e != nil {
			return e
		}
	}
	runtimeBundleCheckpoint("state_after_ownership")
	if e = t.validateParents(uid); e != nil {
		return e
	}
	if name != leaf {
		if e = unix.Renameat2(p.fd, name, p.fd, leaf, unix.RENAME_NOREPLACE); e != nil {
			return e
		}
		if e = unix.Fsync(p.fd); e != nil {
			return e
		}
		runtimeBundleCheckpoint("state_after_promotion")
	}
	if p.absent(leaf + ".state.complete.json") {
		receipt := append(append([]byte{}, t.stateIntent()...), inode...)
		if e = p.write(leaf+".state.complete.json", receipt); e != nil {
			return e
		}
		runtimeBundleCheckpoint("state_after_receipt")
	}
	_, e = t.inspectWritableState(serviceUID, serviceGID)
	return e
}
