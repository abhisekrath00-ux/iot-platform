//go:build windows

package paths

import (
	"os"
	"path/filepath"
)

func base() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "Hexmon", "edge")
}

// Config is the default config file location.
func Config() string { return filepath.Join(base(), "edge-agent.yaml") }

// Data is the default state directory (identity, queue, artifacts).
// HEXMON_DATA_DIR overrides it (portable installs, tests).
func Data() string {
	if d := os.Getenv("HEXMON_DATA_DIR"); d != "" {
		return d
	}
	return filepath.Join(base(), "data")
}
