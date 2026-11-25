//go:build linux

package claim

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
)

func candidateReviewPreparation(dir string, output io.Writer) error {
	store, e := candidateOpenStore(dir, false)
	if e != nil {
		return fmt.Errorf("private preparation unavailable; preserve all state")
	}
	defer store.Close()
	targets := candidateInstallTargets{Binary: filepath.Join(dir, "prepared-agent"), Config: filepath.Join(dir, "prepared-config.yaml"), Unit: filepath.Join(dir, "prepared-unit.service"), State: filepath.Join(dir, "prepared-state"), Destination: filepath.Join(dir, "accepted-identity")}
	if _, e = candidateSnapshotInstallTargets(targets); e != nil {
		return fmt.Errorf("preparation incomplete, altered or unsafe; preserve it, no replay or repair")
	}
	return json.NewEncoder(output).Encode(CandidateReview{Version: 1, State: "private_payload_verified_not_installed", Message: "Prepared private payload and manifest match; state is unidentified. No destination installation, current server authorization, identity acceptance or service startup is established."})
}
