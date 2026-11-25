package claim

// Read-only installer binding evidence. A matching record is never permission
// to copy identity, clear pending state, enroll or start the installed service.
import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

type candidateInstallTargets struct {
	Binary      string
	Config      string
	State       string
	Unit        string
	Destination string
}
type candidateInstallPlan struct {
	Version        int
	Attempt        string
	IntentSHA256   string
	Phase          string
	Targets        candidateInstallTargets
	TargetSnapshot map[string]string
}

// RunCandidateInstallReview reads a pre-existing explicit plan and retained
// material through the same pinned store. It never makes a plan or repairs one.
func RunCandidateInstallReview(input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded installer review input required")
	}
	var in struct {
		Dir, Server, Serial, DeviceCA, ExpectedTenant string
		ResponseKey                                   ed25519.PublicKey
		Targets                                       candidateInstallTargets
	}
	if candidateDecodeRecovery(raw, &in) != nil || in.Dir == "" || in.Server == "" || in.Serial == "" || in.ExpectedTenant == "" || len(in.ResponseKey) != ed25519.PublicKeySize {
		return fmt.Errorf("explicit installer binding and trust required")
	}
	if !candidateStrictInstallTargets(raw) || !candidateValidInstallTargets(in.Dir, in.Targets) {
		return fmt.Errorf("distinct canonical installer targets outside staging required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(in.DeviceCA)) {
		return fmt.Errorf("separate device trust required")
	}
	store, e := candidateOpenStore(in.Dir, false)
	if e != nil {
		return fmt.Errorf("private staged installer review refused")
	}
	defer store.Close()
	intent, key, e := candidateLoadIntent(store, in.Server, in.Serial, in.ExpectedTenant, in.ResponseKey, []byte(in.DeviceCA))
	if e != nil {
		return fmt.Errorf("retained installer attempt refused")
	}
	marker, e := store.Read("candidate-started")
	if e != nil || string(marker) != "install-uncertain\n" {
		return fmt.Errorf("attempt was not staged with an installer binding")
	}
	raw, e = store.Read("candidate-install-plan.json")
	var plan candidateInstallPlan
	if e != nil || candidateDecodeRecovery(raw, &plan) != nil || !candidateStrictInstallTargets(raw) || !candidateStrictInstallSnapshot(raw) || plan.Version != 3 || plan.Attempt != intent.ID || plan.Phase != "prepared" || plan.Targets != in.Targets {
		return fmt.Errorf("installer plan absent, partial or mismatched; preserve all state")
	}
	intentRaw, e := store.Read("candidate-intent.json")
	if e != nil {
		return fmt.Errorf("retained intent unavailable")
	}
	digest := sha256.Sum256(intentRaw)
	if plan.IntentSHA256 != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("installer plan differs from retained intent")
	}
	response, e := store.Read("candidate-response.json")
	if e != nil || candidateVerifyResponse(response, intent, key, roots, in.ResponseKey, in.ExpectedTenant) != nil {
		return fmt.Errorf("verified retained response required; preserve all state")
	}
	if e = candidateCheckInstallSnapshot(plan); e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(CandidateReview{Version: 1, State: "prepared_install_binding_verified", Message: "Prepared installer binding and existing target fingerprints match. No current server authorization or interruption-safe installation is established. Installation, service start and pending-state clearing remain blocked."})
}

func candidateValidInstallTargets(dir string, t candidateInstallTargets) bool {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return false
	}
	paths := []string{dir, t.Binary, t.Config, t.State, t.Unit, t.Destination}
	for i, p := range paths {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == string(filepath.Separator) {
			return false
		}
		for _, q := range paths[:i] {
			rel, e := filepath.Rel(p, q)
			if e != nil || rel == "." || rel != ".." && !isParentRelative(rel) {
				return false
			}
			rel, e = filepath.Rel(q, p)
			if e != nil || rel == "." || rel != ".." && !isParentRelative(rel) {
				return false
			}
		}
	}
	return true
}
func isParentRelative(s string) bool { return len(s) > 3 && s[:3] == ".."+string(filepath.Separator) }

func candidateStrictInstallTargets(raw []byte) bool {
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil {
		return false
	}
	var targets candidateInstallTargets
	return candidateDecodeRecovery(outer["Targets"], &targets) == nil
}

func candidateValidateOptionalInstallPlan(store *candidateStore, intent candidateIntent, dir string) error {
	raw, e := store.Read("candidate-install-plan.json")
	if os.IsNotExist(e) {
		marker, readErr := store.Read("candidate-started")
		if readErr != nil || string(marker) == "install-uncertain\n" {
			return fmt.Errorf("required installer plan missing; no request")
		}
		return nil
	}
	var plan candidateInstallPlan
	if e != nil || candidateDecodeRecovery(raw, &plan) != nil || !candidateStrictInstallTargets(raw) || !candidateStrictInstallSnapshot(raw) || plan.Version != 3 || plan.Attempt != intent.ID || plan.Phase != "prepared" || !candidateValidInstallTargets(dir, plan.Targets) {
		return fmt.Errorf("retained install plan partial or mismatched; no request")
	}
	intentRaw, e := store.Read("candidate-intent.json")
	if e != nil {
		return e
	}
	digest := sha256.Sum256(intentRaw)
	if plan.IntentSHA256 != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("retained install intent differs; no request")
	}
	return candidateCheckInstallSnapshot(plan)
}

func candidateCheckInstallSnapshot(plan candidateInstallPlan) error {
	snapshot, e := candidateSnapshotInstallTargets(plan.Targets)
	if e != nil || len(plan.TargetSnapshot) != 7 || !reflect.DeepEqual(snapshot, plan.TargetSnapshot) {
		return fmt.Errorf("installed targets differ or are unsafe; no install or request")
	}
	return nil
}

func candidateStrictInstallSnapshot(raw []byte) bool {
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil {
		return false
	}
	var snapshot struct {
		Binary      string `json:"binary"`
		Config      string `json:"config"`
		Unit        string `json:"unit"`
		Key         string `json:"identity.key"`
		Identity    string `json:"identity.json"`
		State       string `json:"state_directory"`
		Destination string `json:"destination_parent"`
	}
	return candidateDecodeRecovery(outer["TargetSnapshot"], &snapshot) == nil
}
