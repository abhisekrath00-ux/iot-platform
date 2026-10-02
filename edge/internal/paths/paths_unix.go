//go:build !windows

package paths

import "os"

// Config is the default config file location.
func Config() string { return "/etc/hexmon/edge-agent.yaml" }

// Data is the default state directory (identity, queue, artifacts).
// HEXMON_DATA_DIR overrides it (portable installs, tests).
func Data() string {
	if d := os.Getenv("HEXMON_DATA_DIR"); d != "" {
		return d
	}
	return "/var/lib/hexmon-edge"
}
