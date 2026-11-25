//go:build linux

package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"golang.org/x/sys/unix"
	"io"
	"path/filepath"
)

// RunPrivateRuntimeAssembly commits only private nonexecuting payload data below
// the creation-bound prepared-state. No new local destination can be selected.
func RunPrivateRuntimeAssembly(ctx context.Context, input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 262145))
	if e != nil || len(raw) > 262144 {
		return fmt.Errorf("bounded runtime assembly input required")
	}
	var in struct {
		Retained                       json.RawMessage
		InputDir, Serial, Tenant, User string
	}
	if e = decodeMappingInput(raw, &in); e != nil || in.InputDir == "" || in.Serial == "" || in.Tenant == "" {
		return fmt.Errorf("explicit private runtime assembly and trust required")
	}
	e = claim.WithVerifiedPrivateRuntime(ctx, in.Retained, func(v claim.VerifiedPrivateRuntime) error {
		// Duplicate the verified held state handle, never reopen an independently
		// validated pathname to choose the commit root.
		fd, e := unix.Dup(int(v.StateHandle.Fd()))
		if e != nil {
			return e
		}
		unix.CloseOnExec(fd)
		parent := &runtimeBundleStore{fd}
		defer parent.close()
		if e = ValidateIdentityMaterial(v.Files["identity.crt"], v.Files["identity.key"], v.Files["device-ca.pem"], in.Serial); e != nil {
			return e
		}
		var id claim.Identity
		if json.Unmarshal(v.Files["identity.json"], &id) != nil {
			return fmt.Errorf("verified runtime identity malformed")
		}
		data, e := claim.ReadCandidatePrivateFiles(in.InputDir, "server-config.yaml", "broker-ca.pem")
		if e != nil {
			return fmt.Errorf("private checked runtime inputs unavailable")
		}
		// Review unit remains inert. Agent payload stays0600 and no service unit is
		// installed. Paths name the future same-directory payload and separate state.
		destination := filepath.Join(v.StateDir, "runtime")
		mapping, e := buildRuntimeMappingChecked(data["server-config.yaml"], &id, in.Serial, in.Tenant, v.IdentityDir, filepath.Join(in.InputDir, "broker-ca.pem"), string(data["broker-ca.pem"]), filepath.Join(destination, "agent"), filepath.Join(destination, "config.yaml"), filepath.Join(v.StateDir, "service-data"), in.User)
		if e != nil {
			return e
		}
		files := map[string][]byte{"agent": v.Binary, "config.yaml": []byte(mapping.ConfigYAML), "unit.service": []byte(mapping.Unit)}
		intent := runtimeBundleIntent{Version: 1, Attempt: v.Attempt, Identity: map[string]string{}, Files: map[string]string{}}
		for n, b := range v.Files {
			intent.Identity[n] = runtimeHash(b)
		}
		for n, b := range files {
			intent.Files[n] = runtimeHash(b)
		}
		if e = v.Validate(); e != nil {
			return e
		}
		if e = commitRuntimeBundle(parent, intent, files); e != nil {
			return e
		}
		if v.Deployment == nil {
			return nil
		}
		d := v.Deployment
		// This is a private frozen preview of exact final-path bytes, still0600
		// and nonexecuting under the original creating UID. It is not root trust.
		mapping, e = buildRuntimeMappingWithService(data["server-config.yaml"], &id, in.Serial, in.Tenant, filepath.Join(d.Root, "identity"), filepath.Join(d.Root, "broker-ca.pem"), string(data["broker-ca.pem"]), filepath.Join(d.Root, "agent"), filepath.Join(d.Root, "config.yaml"), d.State, d.ServiceUID, d.ServiceGID, true)
		if e != nil {
			return e
		}
		handoff := map[string][]byte{"agent": v.Binary, "config.yaml": []byte(mapping.ConfigYAML), "unit.service": []byte(mapping.Unit), "broker-ca.pem": data["broker-ca.pem"]}
		for n, b := range v.Files {
			handoff[n] = b
		}
		frozen := runtimeBundleIntent{Version: 2, Attempt: v.Attempt, Identity: intent.Identity, Files: map[string]string{}, Deployment: d}
		for n, b := range handoff {
			frozen.Files[n] = runtimeHash(b)
		}
		if e = v.Validate(); e != nil {
			return e
		}
		return commitNamedRuntimeBundle(parent, frozen, handoff, "handoff")
	})
	if e != nil {
		return fmt.Errorf("private runtime assembly unresolved; preserve all material, no install or start")
	}
	return json.NewEncoder(output).Encode(struct {
		State                                 string
		CanInstall, CanStart, CanClearPending bool
		Message                               string
	}{State: "private_runtime_payload_committed", Message: "Complete private nonexecuting runtime payload verified after fresh retained recovery. Any prebound final-path handoff is private nonexecuting data only. Root ownership handoff, system installation, service start and pending clearing remain blocked."})
}
