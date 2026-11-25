//go:build linux && hex_candidate_fault_acceptance

package setup

import "os"

func runtimeBundleCheckpoint(stage string) {
	if os.Getenv("HEX_CANDIDATE_TEST_CRASH") == stage {
		os.Exit(86)
	}
}
func runtimeBundlePartial(f *os.File, name string, b []byte) {
	if os.Getenv("HEX_CANDIDATE_TEST_CRASH") == "runtime_partial_"+name {
		_, _ = f.Write(b[:len(b)/2])
		_ = f.Sync()
		os.Exit(86)
	}
}
