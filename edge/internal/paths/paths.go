// Package paths gives OS-appropriate default locations so the same agent
// runs on Linux (FHS) and Windows (ProgramData) without flags.
package paths

import "path/filepath"

// Queue is the default SQLite buffer path.
func Queue() string { return filepath.Join(Data(), "queue.db") }

// Artifacts is the default fleet artifact store.
func Artifacts() string { return filepath.Join(Data(), "artifacts") }
