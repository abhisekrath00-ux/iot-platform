//go:build linux

package setup

// Dormant root-origin deployment primitive. No CLI/installer entrypoint calls
// this. It installs stopped data only, never calls systemd, executes a payload,
// enables boot startup, creates users or removes retained material.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type deploymentStore struct {
	fd  int
	uid uint32
}

func (s *deploymentStore) close() { unix.Close(s.fd) }
func openDeploymentParent(path string, uid uint32) (*deploymentStore, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("canonical deployment parent required")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	for _, n := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if n == "" {
			continue
		}
		next, e := unix.Openat(fd, n, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		// Synthetic tests may traverse root-owned ancestors/sticky tmp. The final
		// parent must be exclusively owned by the transaction principal below.
		if e != nil || (st.Uid != uid && st.Uid != 0) || st.Mode&0022 != 0 && st.Mode&unix.S_ISVTX == 0 {
			unix.Close(fd)
			return nil, fmt.Errorf("unsafe deployment ancestry")
		}
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uid || st.Mode&0022 != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("exclusive deployment parent required")
	}
	return &deploymentStore{fd, uid}, nil
}
func (s *deploymentStore) snapshot() string {
	var st unix.Stat_t
	if unix.Fstat(s.fd, &st) != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
func (s *deploymentStore) read(n string, limit int64) ([]byte, error) {
	fd, e := unix.Openat(s.fd, n, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), n)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != s.uid || st.Mode&0777 != 0600 || st.Nlink != 1 || st.Size < 0 || st.Size > limit {
		return nil, fmt.Errorf("private deployment file required")
	}
	b := make([]byte, st.Size)
	nread, e := f.ReadAt(b, 0)
	if e != nil || nread != len(b) {
		return nil, fmt.Errorf("complete deployment file required")
	}
	return b, nil
}
func (s *deploymentStore) write(n string, b []byte) error {
	fd, e := unix.Openat(s.fd, n, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), n)
	runtimeBundlePartial(f, n, b)
	w, e := f.Write(b)
	if e == nil && w != len(b) {
		e = fmt.Errorf("short deployment write")
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
func (s *deploymentStore) dir(n string) (*deploymentStore, error) {
	fd, e := unix.Openat(s.fd, n, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != s.uid || st.Mode&0777 != 0700 {
		unix.Close(fd)
		return nil, fmt.Errorf("private deployment directory required")
	}
	return &deploymentStore{fd, s.uid}, nil
}
func (s *deploymentStore) names() ([]string, error) {
	fd, e := unix.Openat(s.fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "deployment")
	defer f.Close()
	return f.Readdirnames(-1)
}
func (s *deploymentStore) absent(n string) bool {
	var st unix.Stat_t
	return unix.Fstatat(s.fd, n, &st, unix.AT_SYMLINK_NOFOLLOW) == unix.ENOENT
}
func deploymentExactName(n string) bool {
	return n != "" && n != "." && n != ".." && filepath.Base(n) == n
}

type deploymentTransaction struct {
	intent   runtimeBundleIntent
	files    map[string][]byte
	parents  map[string]*deploymentStore
	leaves   map[string]string
	validate func() error
}

// rootDeploymentTransaction requires a root-origin retained attempt. Fresh
// authority and exact mapping must be verified by its caller while original
// handles remain held. Digests or a private receipt never grant that authority.
func rootDeploymentTransaction(v claim.VerifiedPrivateRuntime, intent runtimeBundleIntent, files map[string][]byte) error {
	if os.Geteuid() != 0 || v.Deployment == nil || v.Validate == nil || v.StateHandle == nil {
		return fmt.Errorf("root-origin verified deployment required")
	}
	var st unix.Stat_t
	if unix.Fstat(int(v.StateHandle.Fd()), &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0777 != 0700 || !bytes.Equal(v.Binary, files["agent"]) {
		return fmt.Errorf("root-owned held state and exact prepared executable required")
	}
	return runDeploymentTransaction(v, intent, files, 0)
}
func runDeploymentTransaction(v claim.VerifiedPrivateRuntime, intent runtimeBundleIntent, files map[string][]byte, uid uint32) error {
	return withDeploymentTransaction(v, intent, files, uid, func(t *deploymentTransaction) error { return t.commit(uid) })
}
func inspectDeploymentTransaction(v claim.VerifiedPrivateRuntime, intent runtimeBundleIntent, files map[string][]byte, uid uint32) (string, error) {
	state := ""
	e := withDeploymentTransaction(v, intent, files, uid, func(t *deploymentTransaction) error {
		if e := t.validateParents(uid); e != nil {
			return e
		}
		var e error
		state, e = t.inspect()
		return e
	})
	return state, e
}
func withDeploymentTransaction(v claim.VerifiedPrivateRuntime, intent runtimeBundleIntent, files map[string][]byte, uid uint32, apply func(*deploymentTransaction) error) error {
	d := v.Deployment
	if d == nil || intent.Deployment == nil || d.Activation != "stopped-only" || !validNumericServiceID(d.ServiceUID) || !validNumericServiceID(d.ServiceGID) || intent.Version != 2 || !runtimeValidHash(intent.Attempt) || v.Attempt != intent.Attempt || len(files) != 8 || len(intent.Files) != 8 || len(intent.Identity) != 4 || v.Validate == nil {
		return fmt.Errorf("complete exact stopped deployment required")
	}
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(intent.Deployment)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("deployment contract differs")
	}
	for _, n := range []string{"agent", "config.yaml", "unit.service", "broker-ca.pem", "device-ca.pem", "identity.json", "identity.crt", "identity.key"} {
		if len(files[n]) == 0 || runtimeHash(files[n]) != intent.Files[n] || len(files[n]) > 64<<20 {
			return fmt.Errorf("complete frozen deployment bytes required")
		}
	}
	for _, n := range []string{"device-ca.pem", "identity.json", "identity.crt", "identity.key"} {
		if runtimeHash(files[n]) != intent.Identity[n] || !bytes.Equal(v.Files[n], files[n]) {
			return fmt.Errorf("accepted deployment identity differs")
		}
	}
	// All final targets are nonoverlapping canonical leaves outside retained state.
	paths := []string{v.StateDir, d.Root, d.State, d.Unit}
	for i, p := range paths {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return fmt.Errorf("canonical deployment targets required")
		}
		for _, q := range paths[:i] {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return fmt.Errorf("deployment targets overlap")
			}
		}
	}
	t := &deploymentTransaction{intent: intent, files: files, parents: map[string]*deploymentStore{}, leaves: map[string]string{}, validate: v.Validate}
	defer func() {
		for _, p := range t.parents {
			p.close()
		}
	}()
	for label, path := range map[string]string{"deployment_parent": d.Root, "deployment_state_parent": d.State, "deployment_unit_parent": d.Unit} {
		parent, e := openDeploymentParent(filepath.Dir(path), uid)
		if e != nil {
			return e
		}
		t.parents[label] = parent
		t.leaves[label] = filepath.Base(path)
		if parent.snapshot() != d.ParentSnapshot[label] || !deploymentExactName(t.leaves[label]) {
			return fmt.Errorf("prebound deployment parent differs")
		}
	}
	return apply(t)
}
func (t *deploymentTransaction) commit(uid uint32) error {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	lock := leaf + ".install.lock"
	fd, e := unix.Openat(p.fd, lock, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uid || st.Mode&0777 != 0600 || st.Nlink != 1 {
		return fmt.Errorf("private install lock required")
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fmt.Errorf("deployment busy")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	if e = t.validateParents(uid); e != nil {
		return e
	}
	if _, e = t.inspect(); e != nil {
		return e
	}
	raw, _ := json.Marshal(t.intent)
	old, e := p.read(leaf+".install.intent.json", 131072)
	if os.IsNotExist(e) {
		for _, n := range []string{leaf, leaf + ".install.pending", leaf + ".install.complete.json"} {
			if !p.absent(n) {
				return fmt.Errorf("unbound deployment state")
			}
		}
		if !t.parents["deployment_state_parent"].absent(t.leaves["deployment_state_parent"]) || !t.parents["deployment_unit_parent"].absent(t.leaves["deployment_unit_parent"]) {
			return fmt.Errorf("unbound state or unit")
		}
		if e = p.write(leaf+".install.intent.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("deploy_after_intent")
	} else if e != nil || !bytes.Equal(old, raw) {
		return fmt.Errorf("deployment intent partial or differs")
	}
	// This first stopped phase freezes the complete nested final payload and
	// receipt. Access/ownership/state/unit publication are separate transitions.
	if !t.parents["deployment_state_parent"].absent(t.leaves["deployment_state_parent"]) || !t.parents["deployment_unit_parent"].absent(t.leaves["deployment_unit_parent"]) {
		return fmt.Errorf("unexpected later deployment transition")
	}
	present := !p.absent(leaf)
	pending := !p.absent(leaf + ".install.pending")
	receipt := !p.absent(leaf + ".install.complete.json")
	if present && pending || receipt && !present {
		return fmt.Errorf("inconsistent deployment state")
	}
	if receipt {
		old, e = p.read(leaf+".install.complete.json", 131072)
		if e != nil || !bytes.Equal(old, raw) {
			return fmt.Errorf("deployment receipt differs")
		}
	}
	if !present {
		if !pending {
			if e = unix.Mkdirat(p.fd, leaf+".install.pending", 0700); e != nil {
				return e
			}
			if e = unix.Fsync(p.fd); e != nil {
				return e
			}
			runtimeBundleCheckpoint("deploy_after_directory")
		}
		stage, e := p.dir(leaf + ".install.pending")
		if e != nil {
			return e
		}
		defer stage.close()
		if e = t.fillPayload(stage); e != nil {
			return e
		}
		if e = t.validateParents(uid); e != nil {
			return e
		}
		if e = unix.Renameat2(p.fd, leaf+".install.pending", p.fd, leaf, unix.RENAME_NOREPLACE); e != nil {
			return e
		}
		if e = unix.Fsync(p.fd); e != nil {
			return e
		}
		runtimeBundleCheckpoint("deploy_after_promotion")
	}
	dst, e := p.dir(leaf)
	if e != nil {
		return e
	}
	defer dst.close()
	if e = t.verifyPayload(dst); e != nil {
		return e
	}
	if !receipt {
		if e = p.write(leaf+".install.complete.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("deploy_after_receipt")
	}
	return nil
}

var deploymentOrder = []string{"agent", "config.yaml", "unit.service", "broker-ca.pem", "device-ca.pem", "identity.json", "identity.crt", "identity.key"}

func (t *deploymentTransaction) fillPayload(s *deploymentStore) error {
	names, e := s.names()
	if e != nil {
		return e
	}
	for _, n := range names {
		if n != "agent" && n != "config.yaml" && n != "unit.service" && n != "broker-ca.pem" && n != "identity" {
			return fmt.Errorf("unexpected final payload entry")
		}
	}
	// Identity is a fixed subdirectory, never an external credential reference.
	if s.absent("identity") {
		if e = unix.Mkdirat(s.fd, "identity", 0700); e != nil {
			return e
		}
		if e = unix.Fsync(s.fd); e != nil {
			return e
		}
		runtimeBundleCheckpoint("deploy_after_identity_directory")
	}
	id, e := s.dir("identity")
	if e != nil {
		return e
	}
	defer id.close()
	idnames, e := id.names()
	if e != nil {
		return e
	}
	for _, n := range idnames {
		if n != "device-ca.pem" && n != "identity.json" && n != "identity.crt" && n != "identity.key" {
			return fmt.Errorf("unexpected final identity entry")
		}
	}
	gap := false
	for _, n := range deploymentOrder {
		parent := s
		if strings.HasPrefix(n, "identity.") || n == "device-ca.pem" {
			parent = id
		}
		if parent.absent(n) {
			gap = true
		} else {
			old, e := parent.read(n, int64(len(t.files[n])))
			if e != nil || !bytes.Equal(old, t.files[n]) || gap {
				return fmt.Errorf("partial or nonprefix final payload")
			}
		}
	}
	for _, n := range deploymentOrder {
		parent := s
		if strings.HasPrefix(n, "identity.") || n == "device-ca.pem" {
			parent = id
		}
		if parent.absent(n) {
			if e = parent.write(n, t.files[n]); e != nil {
				return e
			}
			runtimeBundleCheckpoint("deploy_after_" + n)
		}
	}
	return t.verifyPayload(s)
}
func (t *deploymentTransaction) verifyPayload(s *deploymentStore) error {
	names, e := s.names()
	if e != nil || len(names) != 5 {
		return fmt.Errorf("final payload pathset differs")
	}
	id, e := s.dir("identity")
	if e != nil {
		return e
	}
	defer id.close()
	names, e = id.names()
	if e != nil || len(names) != 4 {
		return fmt.Errorf("final identity pathset differs")
	}
	for _, n := range deploymentOrder {
		p := s
		if strings.HasPrefix(n, "identity.") || n == "device-ca.pem" {
			p = id
		}
		b, e := p.read(n, int64(len(t.files[n])))
		if e != nil || !bytes.Equal(b, t.files[n]) {
			return fmt.Errorf("final frozen payload differs")
		}
	}
	return nil
}

// Kept separate from storage: access transition must verify these exact IDs.
func deploymentServiceIDs(d *claim.VerifiedDeployment) (int, int, error) {
	if d == nil || !validNumericServiceID(d.ServiceUID) || !validNumericServiceID(d.ServiceGID) {
		return 0, 0, fmt.Errorf("nonroot service IDs required")
	}
	u, _ := strconv.ParseUint(d.ServiceUID, 10, 32)
	g, _ := strconv.ParseUint(d.ServiceGID, 10, 32)
	return int(u), int(g), nil
}

// Reopen exact selected parent paths and compare to held descriptors immediately
// before intent/promotion. Held descriptors alone do not pin a pathname.
func (t *deploymentTransaction) validateParents(uid uint32) error {
	d := t.intent.Deployment
	for label, path := range map[string]string{"deployment_parent": d.Root, "deployment_state_parent": d.State, "deployment_unit_parent": d.Unit} {
		current, e := openDeploymentParent(filepath.Dir(path), uid)
		if e != nil {
			return e
		}
		same := current.snapshot() == t.parents[label].snapshot() && current.snapshot() == d.ParentSnapshot[label]
		current.close()
		if !same {
			return fmt.Errorf("deployment parent path replaced")
		}
	}
	return t.validate()
}

// inspect is strictly read-only. It classifies only the exact frozen attempt's
// complete-file prefixes. Caller must separately refresh server authorization;
// a local intent or complete receipt is never treated as authorization.
func (t *deploymentTransaction) inspect() (string, error) {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	if !p.absent(leaf + ".install.lock") {
		fd, e := unix.Openat(p.fd, leaf+".install.lock", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if e != nil {
			return "", e
		}
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		unix.Close(fd)
		if e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != p.uid || st.Nlink != 1 || st.Mode&0777 != 0600 {
			return "", fmt.Errorf("unsafe deployment lock")
		}
	}
	raw, _ := json.Marshal(t.intent)
	for _, label := range []string{"deployment_state_parent", "deployment_unit_parent"} {
		if !t.parents[label].absent(t.leaves[label]) {
			return "", fmt.Errorf("unexpected deployment state/unit")
		}
	}
	old, e := p.read(leaf+".install.intent.json", 131072)
	present := !p.absent(leaf)
	pending := !p.absent(leaf + ".install.pending")
	receipt := !p.absent(leaf + ".install.complete.json")
	if os.IsNotExist(e) {
		if present || pending || receipt {
			return "", fmt.Errorf("unbound deployment prefix")
		}
		return "unstarted", nil
	}
	if e != nil || !bytes.Equal(old, raw) || present && pending || receipt && !present {
		return "", fmt.Errorf("inconsistent or foreign deployment prefix")
	}
	if receipt {
		old, e = p.read(leaf+".install.complete.json", 131072)
		if e != nil || !bytes.Equal(old, raw) {
			return "", fmt.Errorf("partial or foreign deployment receipt")
		}
	}
	if present {
		dst, e := p.dir(leaf)
		if e != nil {
			return "", e
		}
		defer dst.close()
		if e = t.verifyPayload(dst); e != nil {
			return "", e
		}
		if receipt {
			return "complete_private_payload", nil
		}
		return "promoted_without_receipt", nil
	}
	if !pending {
		return "intent_only", nil
	}
	stage, e := p.dir(leaf + ".install.pending")
	if e != nil {
		return "", e
	}
	defer stage.close()
	names, e := stage.names()
	if e != nil {
		return "", e
	}
	for _, n := range names {
		if n != "agent" && n != "config.yaml" && n != "unit.service" && n != "broker-ca.pem" && n != "identity" {
			return "", fmt.Errorf("unknown staged payload entry")
		}
	}
	if stage.absent("identity") {
		if len(names) != 0 {
			return "", fmt.Errorf("staged identity directory gap")
		}
		return "empty_stage", nil
	}
	id, e := stage.dir("identity")
	if e != nil {
		return "", e
	}
	defer id.close()
	names, e = id.names()
	if e != nil {
		return "", e
	}
	for _, n := range names {
		if n != "device-ca.pem" && n != "identity.json" && n != "identity.crt" && n != "identity.key" {
			return "", fmt.Errorf("unknown staged identity entry")
		}
	}
	gap := false
	count := 0
	for _, n := range deploymentOrder {
		parent := stage
		if strings.HasPrefix(n, "identity.") || n == "device-ca.pem" {
			parent = id
		}
		if parent.absent(n) {
			gap = true
			continue
		}
		old, e := parent.read(n, int64(len(t.files[n])))
		if e != nil || !bytes.Equal(old, t.files[n]) || gap {
			return "", fmt.Errorf("partial or nonprefix staged payload")
		}
		count++
	}
	return fmt.Sprintf("complete_file_prefix_%d", count), nil
}
