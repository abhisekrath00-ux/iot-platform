//go:build linux

package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"golang.org/x/sys/unix"
	"os"
)

// recoverRootDeployment is deliberately not exposed by any product command.
// A future exact native gate must verify the joined root-origin boundary before
// enabling it. It may commit a stopped unit last, never reloads/starts/enables.
func recoverRootDeployment(ctx context.Context, raw []byte) error {
	inspect := func(v claim.VerifiedPrivateRuntime) error {
		i, f, e := readVerifiedDeploymentHandoff(v)
		if e != nil {
			return e
		}
		serviceUID, gid, e := deploymentServiceIDs(v.Deployment)
		if e != nil {
			return e
		}
		return withDeploymentTransaction(v, i, f, 0, func(t *deploymentTransaction) error {
			if e := t.validateParents(0); e != nil {
				return e
			}
			_, e := t.inspectStoppedDeployment(uint32(serviceUID), uint32(gid))
			return e
		})
	}
	return claim.WithVerifiedDeploymentRecovery(ctx, raw, inspect, func(v claim.VerifiedPrivateRuntime) error {
		i, f, e := readVerifiedDeploymentHandoff(v)
		if e != nil {
			return e
		}
		// Fresh recovery may be resuming permissions after final promotion.
		serviceUID, gid, e := deploymentServiceIDs(v.Deployment)
		if e != nil {
			return e
		}
		if os.Geteuid() != 0 || v.StateHandle == nil {
			return fmt.Errorf("root-origin held state required")
		}
		var st unix.Stat_t
		if unix.Fstat(int(v.StateHandle.Fd()), &st) != nil || st.Uid != 0 || st.Mode&0777 != 0700 {
			return fmt.Errorf("root-owned verified state required")
		}
		return withDeploymentTransaction(v, i, f, 0, func(t *deploymentTransaction) error {
			return t.commitStoppedDeployment(0, uint32(serviceUID), uint32(gid))
		})
	})
}

func readVerifiedDeploymentHandoff(v claim.VerifiedPrivateRuntime) (runtimeBundleIntent, map[string][]byte, error) {
	i := runtimeBundleIntent{Deployment: &claim.VerifiedDeployment{}}
	if v.StateHandle == nil || v.Deployment == nil {
		return i, nil, fmt.Errorf("held prebound deployment state required")
	}
	fd, e := unix.Dup(int(v.StateHandle.Fd()))
	if e != nil {
		return i, nil, e
	}
	unix.CloseOnExec(fd)
	root := &runtimeBundleStore{fd}
	defer root.close()
	raw, e := root.read("handoff.intent.json", 131072)
	if e != nil {
		return i, nil, e
	}
	saved, e := root.read("handoff.complete.json", 131072)
	if e != nil || !bytes.Equal(raw, saved) || decodeMappingInput(raw, &i) != nil || i.Version != 2 || i.Attempt != v.Attempt || i.Deployment == nil {
		return i, nil, fmt.Errorf("complete same-attempt handoff receipt required")
	}
	a, _ := json.Marshal(v.Deployment)
	b, _ := json.Marshal(i.Deployment)
	if !bytes.Equal(a, b) {
		return i, nil, fmt.Errorf("handoff final targets differ")
	}
	s, e := runtimeBundleOpenAt(root.fd, "handoff")
	if e != nil {
		return i, nil, e
	}
	defer s.close()
	names, e := runtimeBundleNames(s)
	if e != nil || len(names) != 8 || len(i.Files) != 8 || len(i.Identity) != 4 {
		return i, nil, fmt.Errorf("complete handoff pathset required")
	}
	files := map[string][]byte{}
	for _, n := range deploymentOrder {
		limit := int64(2 << 20)
		if n == "agent" {
			limit = 64 << 20
		}
		data, e := s.read(n, limit)
		if e != nil || runtimeHash(data) != i.Files[n] {
			return i, nil, fmt.Errorf("frozen handoff differs")
		}
		files[n] = data
	}
	if !bytes.Equal(files["agent"], v.Binary) {
		return i, nil, fmt.Errorf("handoff executable differs from prepared product")
	}
	for _, n := range []string{"device-ca.pem", "identity.json", "identity.crt", "identity.key"} {
		if !bytes.Equal(files[n], v.Files[n]) || runtimeHash(files[n]) != i.Identity[n] {
			return i, nil, fmt.Errorf("handoff differs from accepted identity")
		}
	}
	return i, files, nil
}
