//go:build !windows

package claim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Snapshot existing installation inputs through no-follow pinned directories.
// This is read-only evidence, not a commit lock or same-user/root protection.
func candidateSnapshotInstallTargets(targets candidateInstallTargets) (map[string]string, error) {
	out := map[string]string{}
	deployment, e := candidateSnapshotDeploymentTargets(targets)
	if e != nil {
		return nil, e
	}
	for n, v := range deployment {
		out[n] = v
	}
	for label, path := range map[string]string{"binary": targets.Binary, "config": targets.Config, "unit": targets.Unit} {
		fd, e := candidateOpenTargetDirectory(filepath.Dir(path))
		if e != nil {
			return nil, e
		}
		hash, e := candidateHashTarget(fd, filepath.Base(path), 64<<20, false)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		out[label] = hash
	}
	fd, e := candidateOpenTargetDirectory(targets.State)
	if e != nil {
		return nil, e
	}
	defer unix.Close(fd)
	for _, name := range []string{"identity.key", "identity.json"} {
		hash, e := candidateHashTarget(fd, name, 131072, true)
		if os.IsNotExist(e) {
			out[name] = "absent"
			continue
		}
		if e != nil {
			return nil, e
		}
		_ = hash
		return nil, fmt.Errorf("existing installed identity refuses new retained enrollment")
	}
	for _, name := range []string{"identity.crt", "ca.pem", "device-ca.pem", "candidate-started", "request-started", "candidate-intent.json", "candidate-response.json", "intent.json", "response.json"} {
		_, e := candidateHashTarget(fd, name, 131072, true)
		if !os.IsNotExist(e) {
			return nil, fmt.Errorf("existing or uncertain installed identity refuses enrollment")
		}
	}
	// A directory identifier detects a replaced state directory even when its
	// identity files have been copied exactly. Rechecking still is not a lock.
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return nil, e
	}
	out["state_directory"] = fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	destinationParent, e := candidateOpenTargetDirectory(filepath.Dir(targets.Destination))
	if e != nil {
		return nil, e
	}
	var destinationStat unix.Stat_t
	e = unix.Fstat(destinationParent, &destinationStat)
	unix.Close(destinationParent)
	if e != nil {
		return nil, e
	}
	out["destination_parent"] = fmt.Sprintf("%d:%d", destinationStat.Dev, destinationStat.Ino)

	if filepath.Base(targets.Binary) == "prepared-agent" || filepath.Base(targets.State) == "prepared-state" {
		root := filepath.Dir(targets.Binary)
		if targets.Config != filepath.Join(root, "prepared-config.yaml") || targets.Unit != filepath.Join(root, "prepared-unit.service") || targets.State != filepath.Join(root, "prepared-state") {
			return nil, fmt.Errorf("prepared payload targets differ")
		}
		store, e := candidateOpenStore(root, false)
		if e != nil {
			return nil, e
		}
		raw, e := store.Read("prepared-manifest.json")
		store.Close()
		var manifest struct {
			Version int
			Phase   string
			Files   map[string]string
		}
		if e != nil || candidateDecodeRecovery(raw, &manifest) != nil || manifest.Version != 1 || manifest.Phase != "payload_prepared_not_installed" || len(manifest.Files) != 3 {
			return nil, fmt.Errorf("preparation manifest missing or partial")
		}
		var outer map[string]json.RawMessage
		if json.Unmarshal(raw, &outer) != nil {
			return nil, fmt.Errorf("preparation manifest invalid")
		}
		var names struct {
			Binary string `json:"prepared-agent"`
			Config string `json:"prepared-config.yaml"`
			Unit   string `json:"prepared-unit.service"`
		}
		if candidateDecodeRecovery(outer["Files"], &names) != nil {
			return nil, fmt.Errorf("preparation manifest ambiguous")
		}
		for label, name := range map[string]string{"binary": "prepared-agent", "config": "prepared-config.yaml", "unit": "prepared-unit.service"} {
			hash := out[label]
			parts := strings.Split(hash, ":")
			if len(parts) != 3 || parts[2] != manifest.Files[name] {
				return nil, fmt.Errorf("prepared payload differs from manifest")
			}
		}
	}
	return out, nil
}
func candidateOpenTargetDirectory(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, fmt.Errorf("canonical target directory required")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, e
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, name := range parts {
		next, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return -1, e
		}
		fd = next
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		owned := st.Uid == uint32(os.Getuid()) || st.Uid == 0
		safe := e == nil && owned && st.Mode&unix.S_IFMT == unix.S_IFDIR
		if st.Mode&0022 != 0 {
			safe = safe && i < len(parts)-1 && st.Mode&unix.S_ISVTX != 0
		}
		if !safe {
			unix.Close(fd)
			return -1, fmt.Errorf("unsafe installed target directory")
		}
	}
	return fd, nil
}
func candidateHashTarget(dir int, name string, max int64, private bool) (string, error) {
	if !candidateExactName(name) {
		return "", fmt.Errorf("exact installed target filename required")
	}
	fd, e := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return "", e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var before, after unix.Stat_t
	if e = unix.Fstat(fd, &before); e != nil {
		return "", e
	}
	safe := before.Mode&unix.S_IFMT == unix.S_IFREG && (before.Uid == uint32(os.Getuid()) || before.Uid == 0) && before.Nlink == 1 && before.Mode&0022 == 0 && before.Size >= 0 && before.Size <= max
	if private {
		safe = safe && before.Mode&0077 == 0
	}
	if !safe {
		return "", fmt.Errorf("unsafe or oversized installed target")
	}
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(f, max+1))
	if e != nil || n > max {
		return "", fmt.Errorf("installed target unreadable or grew")
	}
	if e = unix.Fstat(fd, &after); e != nil {
		return "", e
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return "", fmt.Errorf("installed target changed during read")
	}
	return fmt.Sprintf("%d:%d:%s", after.Dev, after.Ino, hex.EncodeToString(hash.Sum(nil))), nil
}

// Held read-only parent handles do not lock files. Validate reopens the exact
// selected paths and compares parent identities as well as content baselines.
type candidateInstallTargetGuard struct {
	plan  candidateInstallPlan
	dirs  map[string]int
	check func(candidateInstallPlan) error
}

func candidateHoldInstallTargets(plan candidateInstallPlan) (*candidateInstallTargetGuard, error) {
	return candidateHoldInstallTargetsChecked(plan, candidateCheckInstallSnapshot)
}
func candidateHoldInstallTargetsChecked(plan candidateInstallPlan, check func(candidateInstallPlan) error) (*candidateInstallTargetGuard, error) {
	g := &candidateInstallTargetGuard{plan: plan, dirs: map[string]int{}, check: check}
	paths := []string{filepath.Dir(plan.Targets.Binary), filepath.Dir(plan.Targets.Config), filepath.Dir(plan.Targets.Unit), plan.Targets.State, filepath.Dir(plan.Targets.Destination)}
	if candidateHasDeployment(plan.Targets) {
		paths = append(paths, filepath.Dir(plan.Targets.DeploymentRoot), filepath.Dir(plan.Targets.DeploymentState), filepath.Dir(plan.Targets.DeploymentUnit))
	}
	for _, p := range paths {
		if _, ok := g.dirs[p]; ok {
			continue
		}
		fd, e := candidateOpenTargetDirectory(p)
		if e != nil {
			g.Close()
			return nil, e
		}
		g.dirs[p] = fd
	}
	if e := g.Validate(); e != nil {
		g.Close()
		return nil, e
	}
	return g, nil
}
func (g *candidateInstallTargetGuard) Close() {
	for p, fd := range g.dirs {
		unix.Close(fd)
		delete(g.dirs, p)
	}
}
func (g *candidateInstallTargetGuard) Validate() error {
	for p, fd := range g.dirs {
		next, e := candidateOpenTargetDirectory(p)
		if e != nil {
			return e
		}
		var held, current unix.Stat_t
		e = unix.Fstat(fd, &held)
		if e == nil {
			e = unix.Fstat(next, &current)
		}
		unix.Close(next)
		if e != nil || held.Dev != current.Dev || held.Ino != current.Ino {
			return fmt.Errorf("installed parent directory replaced")
		}
	}
	return g.check(g.plan)
}

func candidateRequireNewDestination(targets candidateInstallTargets) error {
	fd, e := candidateOpenTargetDirectory(filepath.Dir(targets.Destination))
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	leaf := filepath.Base(targets.Destination)
	for _, name := range []string{leaf, leaf + ".intent.json", leaf + ".pending", leaf + ".complete.json", leaf + ".lock"} {
		var st unix.Stat_t
		e = unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if e != unix.ENOENT {
			return fmt.Errorf("new acceptance destination has prior state")
		}
	}
	return nil
}

// Final targets are exclusive new leaves. Snapshot binds all existing parents;
// identity and payload cannot be installed here by this read-only code.
func candidateSnapshotDeploymentTargets(t candidateInstallTargets) (map[string]string, error) {
	out := map[string]string{}
	if !candidateHasDeployment(t) {
		return out, nil
	}
	if t.Activation != "stopped-only" || !candidateValidServiceID(t.ServiceUID) || !candidateValidServiceID(t.ServiceGID) {
		return nil, fmt.Errorf("complete stopped deployment contract required")
	}
	for label, path := range map[string]string{"deployment_parent": t.DeploymentRoot, "deployment_state_parent": t.DeploymentState, "deployment_unit_parent": t.DeploymentUnit} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return nil, fmt.Errorf("canonical final target required")
		}
		fd, e := candidateOpenTargetDirectory(filepath.Dir(path))
		if e != nil {
			return nil, e
		}
		var st unix.Stat_t
		e = unix.Fstatat(fd, filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW)
		if e != unix.ENOENT {
			unix.Close(fd)
			return nil, fmt.Errorf("final deployment target already exists or is unverifiable")
		}
		e = unix.Fstat(fd, &st)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		out[label] = fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	}
	return out, nil
}
