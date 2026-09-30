//go:build !windows

package paths

// Config is the default config file location.
func Config() string { return "/etc/hexmon/edge-agent.yaml" }

// Data is the default state directory (identity, queue, artifacts).
func Data() string { return "/var/lib/hexmon-edge" }
