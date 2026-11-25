package claim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// RunCandidatePrepareInstall creates only an explicit new private payload.
// It never writes system paths, enrolls, installs a unit or starts a service.
func RunCandidatePrepareInstall(input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded preparation input required")
	}
	var in struct{ Dir string }
	if candidateDecodeRecovery(raw, &in) != nil || in.Dir == "" {
		return fmt.Errorf("explicit new private preparation directory required")
	}
	binary, e := candidateReadOwnExecutable()
	if e != nil {
		return fmt.Errorf("exact running executable could not be prepared")
	}
	store, e := candidateOpenStore(in.Dir, true)
	if e != nil {
		return fmt.Errorf("preparation exists or is unsafe; preserve it, do not overwrite")
	}
	defer store.Close()
	candidateFaultCheckpoint("prepare_after_directory")
	config := []byte("# Unidentified prepared payload. No broker, devices or service start.\ngateway_id: \"\"\ntenant_id: \"\"\nserial: \"\"\ndevices: []\nallowed_commands: []\n")
	unit := []byte("# Dormant review payload only. Not installed or enabled.\n[Unit]\nDescription=HexThings prepared installation, not ready\n[Service]\nType=oneshot\nExecStart=/bin/false\n")
	files := map[string][]byte{"prepared-agent": binary, "prepared-config.yaml": config, "prepared-unit.service": unit}
	hashes := map[string]string{}
	for _, name := range []string{"prepared-agent", "prepared-config.yaml", "prepared-unit.service"} {
		if e = store.Write(name, files[name]); e != nil {
			return e
		}
		sum := sha256.Sum256(files[name])
		hashes[name] = hex.EncodeToString(sum[:])
		candidateFaultCheckpoint("prepare_after_" + name)
	}
	if e = store.MakePrivateDirectory("prepared-state"); e != nil {
		return e
	}
	candidateFaultCheckpoint("prepare_after_state")
	manifest, _ := json.Marshal(struct {
		Version int
		Phase   string
		Files   map[string]string
	}{1, "payload_prepared_not_installed", hashes})
	if e = store.Write("prepared-manifest.json", manifest); e != nil {
		return e
	}
	candidateFaultCheckpoint("prepare_after_manifest")
	return json.NewEncoder(output).Encode(CandidateReview{Version: 1, State: "private_payload_prepared", Message: "Private executable, dormant configuration and unit payload prepared. Nothing was installed, enrolled, enabled or started. Interrupted preparation is never overwritten."})
}

// RunCandidateReviewPreparation classifies only complete verified payloads.
// Partial/tampered input refuses without repair, replay or destination writes.
func RunCandidateReviewPreparation(input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded preparation review input required")
	}
	var in struct{ Dir string }
	if candidateDecodeRecovery(raw, &in) != nil || in.Dir == "" {
		return fmt.Errorf("explicit preparation root required")
	}
	return candidateReviewPreparation(in.Dir, output)
}
