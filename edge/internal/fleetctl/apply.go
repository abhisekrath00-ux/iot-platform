// apply.go: apply a verified config artifact with backup, validation, and
// automatic rollback. The artifact is an edge-agent YAML config. Apply never
// leaves the gateway unconfigured: any validation failure restores the
// previous config byte-for-byte before the caller restarts poll loops.
package fleetctl

import (
	"fmt"
	"os"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// ApplyResult describes an apply attempt for the ACK detail line.
type ApplyResult struct {
	Applied    bool
	BackupPath string
	Devices    int
	Err        error
}

// ApplyConfig validates and installs artifactPath as the agent config at
// configPath. Steps: validate the new YAML loads and has at least the
// identity fields; back up the current config; atomic-write the new one;
// re-validate from disk. Any failure restores the backup and reports it.
// Binary artifacts (agent self-update) are out of scope here; see
// docs/fleet.md for the signing requirement before those ship.
func ApplyConfig(artifactPath, configPath string) ApplyResult {
	res := ApplyResult{}
	fail := func(err error) ApplyResult { res.Err = err; return res }

	newCfg, err := config.Load(artifactPath)
	if err != nil {
		return fail(fmt.Errorf("new config invalid: %w", err))
	}
	if newCfg.GatewayID == "" || newCfg.TenantID == "" {
		return fail(fmt.Errorf("new config missing gateway identity"))
	}

	cur, err := os.ReadFile(configPath)
	if err != nil {
		return fail(fmt.Errorf("current config unreadable: %w", err))
	}
	backup := configPath + ".fleetbak"
	if err := os.WriteFile(backup, cur, 0640); err != nil {
		return fail(fmt.Errorf("backup failed: %w", err))
	}
	res.BackupPath = backup

	if err := writeAtomic(configPath, cur); err != nil { // sanity: temp+rename works before we rely on it
		return fail(fmt.Errorf("config path not writable: %w", err))
	}
	newBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		return fail(err)
	}
	if err := writeAtomic(configPath, newBytes); err != nil {
		os.WriteFile(configPath, cur, 0640) // best-effort restore
		return fail(fmt.Errorf("install failed: %w", err))
	}
	// Re-validate from disk: what the agent will actually load on reload.
	if _, err := config.Load(configPath); err != nil {
		os.WriteFile(configPath, cur, 0640)
		return fail(fmt.Errorf("installed config failed re-validation, rolled back: %w", err))
	}
	res.Applied = true
	res.Devices = len(newCfg.Devices)
	return res
}

// Rollback restores the backup produced by ApplyConfig.
func Rollback(configPath, backupPath string) error {
	b, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("backup unreadable: %w", err)
	}
	return writeAtomic(configPath, b)
}

func writeAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
