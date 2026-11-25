//go:build linux

package setup

// Dormant access transition. Caller must hold the install transaction lock and
// freshly verify the same retained deployment. It does not start/reload/enable.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

type deploymentAccessStep struct {
	Name      string
	Directory bool
	Mode      uint32
}

var deploymentAccessOrder = []deploymentAccessStep{
	{"agent", false, 0750}, {"config.yaml", false, 0640}, {"unit.service", false, 0640}, {"broker-ca.pem", false, 0640},
	{"identity/device-ca.pem", false, 0640}, {"identity/identity.json", false, 0640}, {"identity/identity.crt", false, 0640}, {"identity/identity.key", false, 0640},
	{"identity", true, 0750}, {".", true, 0750},
}

func deploymentOpenAccessAt(s *deploymentStore, step deploymentAccessStep) (int, error) {
	parent := s.fd
	var child int = -1
	name := step.Name
	if strings.HasPrefix(name, "identity/") {
		var e error
		child, e = unix.Openat(parent, "identity", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return -1, e
		}
		defer unix.Close(child)
		parent = child
		name = strings.TrimPrefix(name, "identity/")
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if step.Directory {
		flags |= unix.O_DIRECTORY
	}
	return unix.Openat(parent, name, flags, 0)
}

// Each ordered step has two durable complete states: root-owned old mode with
// the exact new group, then exact new mode. A cut between chown/chmod is known;
// no writable service-owned payload or unknown owner/group/mode is accepted.
func (t *deploymentTransaction) inspectAccessPayload(s *deploymentStore, serviceGID uint32) (int, error) {
	top, e := s.names()
	if e != nil || len(top) != 5 {
		return 0, fmt.Errorf("access payload pathset differs")
	}
	idfd, e := unix.Openat(s.fd, "identity", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return 0, e
	}
	id := &deploymentStore{idfd, s.uid}
	defer id.close()
	names, e := id.names()
	if e != nil || len(names) != 4 {
		return 0, fmt.Errorf("access identity pathset differs")
	}
	prefix := 0
	gap := false
	for index, step := range deploymentAccessOrder {
		fd, e := deploymentOpenAccessAt(s, step)
		if e != nil {
			return 0, e
		}
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		kind := uint32(unix.S_IFREG)
		oldMode := uint32(0600)
		if step.Directory {
			kind = unix.S_IFDIR
			oldMode = 0700
		}
		if e != nil || st.Mode&unix.S_IFMT != kind || st.Uid != s.uid || !step.Directory && st.Nlink != 1 {
			unix.Close(fd)
			return 0, fmt.Errorf("unsafe access payload object")
		}
		phase := 0
		if st.Gid == serviceGID && st.Mode&07777 == step.Mode {
			phase = 2
		} else if st.Gid == serviceGID && st.Mode&07777 == oldMode {
			phase = 1
		} else if st.Gid != uint32(os.Getgid()) || st.Mode&07777 != oldMode {
			unix.Close(fd)
			return 0, fmt.Errorf("unknown access group/mode")
		}
		if !step.Directory {
			n := step.Name
			if strings.HasPrefix(n, "identity/") {
				n = strings.TrimPrefix(n, "identity/")
			}
			want := t.files[n]
			if st.Size != int64(len(want)) {
				unix.Close(fd)
				return 0, fmt.Errorf("access payload length differs")
			}
			f := os.NewFile(uintptr(fd), step.Name)
			b := make([]byte, len(want))
			count, err := f.ReadAt(b, 0)
			f.Close()
			if err != nil || count != len(b) || !bytes.Equal(b, want) {
				return 0, fmt.Errorf("access payload bytes differ")
			}
		} else {
			unix.Close(fd)
		}
		// When synthetic original and service group coincide, an unchanged old mode
		// is old, not evidence that the next step's chown already happened.
		if serviceGID == uint32(os.Getgid()) && phase == 1 {
			phase = 0
		}
		if phase == 2 {
			if gap {
				return 0, fmt.Errorf("access prefix gap")
			}
			prefix = 2 * (index + 1)
		} else {
			if phase == 1 {
				if gap {
					return 0, fmt.Errorf("access chown prefix gap")
				}
				prefix = 2*index + 1
			}
			gap = true
		}
	}
	return prefix, nil
}
func (t *deploymentTransaction) commitAccessPayload(s *deploymentStore, serviceGID uint32) error {
	if _, e := t.inspectAccessPayload(s, serviceGID); e != nil {
		return e
	}
	for _, step := range deploymentAccessOrder {
		fd, e := deploymentOpenAccessAt(s, step)
		if e != nil {
			return e
		}
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil {
			unix.Close(fd)
			return e
		}
		if st.Gid != serviceGID {
			if e = unix.Fchown(fd, -1, int(serviceGID)); e != nil {
				unix.Close(fd)
				return e
			}
			if e = unix.Fsync(fd); e != nil {
				unix.Close(fd)
				return e
			}
		}
		runtimeBundleCheckpoint("access_after_group_" + step.Name)
		if st.Mode&07777 != step.Mode {
			if e = unix.Fchmod(fd, step.Mode); e != nil {
				unix.Close(fd)
				return e
			}
			if e = unix.Fsync(fd); e != nil {
				unix.Close(fd)
				return e
			}
		}
		unix.Close(fd)
		runtimeBundleCheckpoint("access_after_mode_" + step.Name)
	}
	prefix, e := t.inspectAccessPayload(s, serviceGID)
	if e != nil || prefix != 2*len(deploymentAccessOrder) {
		return fmt.Errorf("access payload incomplete")
	}
	return nil
}

// access intent is bound byte-for-byte to original complete payload receipt.
// It is data only, not authority to change ownership or execute its contents.
func deploymentAccessIntent(i runtimeBundleIntent) []byte {
	raw, _ := json.Marshal(struct {
		Version int
		Phase   string
		Payload runtimeBundleIntent
	}{1, "stopped_service_access", i})
	return raw
}

// Access phase extends only the exact completed private payload. Its own
// intent is fsynced before any permission change; partial receipt is preserved.
func (t *deploymentTransaction) inspectStoppedAccess(gid uint32) (string, error) {
	return t.inspectStoppedAccessChecked(gid, false)
}
func (t *deploymentTransaction) inspectStoppedAccessChecked(gid uint32, allowState bool) (string, error) {
	return t.inspectStoppedAccessLater(gid, allowState, false)
}
func (t *deploymentTransaction) inspectStoppedAccessLater(gid uint32, allowState, allowUnit bool) (string, error) {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	raw := deploymentAccessIntent(t.intent)
	intent, e := p.read(leaf+".access.intent.json", 131072)
	if os.IsNotExist(e) {
		if !p.absent(leaf + ".access.complete.json") {
			return "", fmt.Errorf("unbound access receipt")
		}
		return t.inspect()
	}
	if e != nil || !bytes.Equal(intent, raw) {
		return "", fmt.Errorf("access intent partial or differs")
	}
	original, _ := json.Marshal(t.intent)
	for _, n := range []string{leaf + ".install.intent.json", leaf + ".install.complete.json"} {
		b, e := p.read(n, 131072)
		if e != nil || !bytes.Equal(b, original) {
			return "", fmt.Errorf("access requires exact complete payload receipt")
		}
	}
	if !p.absent(leaf + ".install.pending") {
		return "", fmt.Errorf("access with pending payload")
	}
	for _, label := range []string{"deployment_state_parent", "deployment_unit_parent"} {
		if allowState && label == "deployment_state_parent" || allowUnit && label == "deployment_unit_parent" {
			continue
		}
		if !t.parents[label].absent(t.leaves[label]) {
			return "", fmt.Errorf("unbound later state/unit transition")
		}
	}
	fd, e := unix.Openat(p.fd, leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return "", e
	}
	s := &deploymentStore{fd, p.uid}
	defer s.close()
	prefix, e := t.inspectAccessPayload(s, gid)
	if e != nil {
		return "", e
	}
	receipt, e := p.read(leaf+".access.complete.json", 131072)
	if os.IsNotExist(e) {
		return fmt.Sprintf("access_prefix_%d", prefix), nil
	}
	if e != nil || !bytes.Equal(receipt, raw) || prefix != 20 {
		return "", fmt.Errorf("access receipt partial or inconsistent")
	}
	return "complete_stopped_service_access", nil
}
func (t *deploymentTransaction) withInstallLock(uid uint32, apply func() error) error {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	fd, e := unix.Openat(p.fd, leaf+".install.lock", unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uid || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Mode&0777 != 0600 {
		return fmt.Errorf("safe existing deployment lock required")
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fmt.Errorf("deployment busy")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	return apply()
}
func (t *deploymentTransaction) commitStoppedAccess(uid, gid uint32) error {
	return t.withInstallLock(uid, func() error { return t.commitStoppedAccessLocked(uid, gid) })
}
func (t *deploymentTransaction) commitStoppedAccessLocked(uid, gid uint32) error {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]

	if e := t.validateParents(uid); e != nil {
		return e
	}
	state, e := t.inspectStoppedAccess(gid)
	if e != nil {
		return e
	}
	raw := deploymentAccessIntent(t.intent)
	if state == "complete_private_payload" {
		if e = p.write(leaf+".access.intent.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("access_after_intent")
	} else if !strings.HasPrefix(state, "access_prefix_") && state != "complete_stopped_service_access" {
		return fmt.Errorf("complete private payload required before service access")
	}
	payloadFD, e := unix.Openat(p.fd, leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	s := &deploymentStore{payloadFD, uid}
	defer s.close()
	if e = t.commitAccessPayload(s, gid); e != nil {
		return e
	}
	if e = t.validateParents(uid); e != nil {
		return e
	}
	if p.absent(leaf + ".access.complete.json") {
		if e = p.write(leaf+".access.complete.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("access_after_receipt")
	}
	_, e = t.inspectStoppedAccess(gid)
	return e
}
