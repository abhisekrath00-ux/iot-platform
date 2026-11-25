//go:build hex_candidate_fault_acceptance

package claim

// Test-build-only process termination. Ordinary product binaries contain no
// environment-controlled fault behavior. Never use on owner state.
import "os"

func candidateFaultCheckpoint(stage string) {
	if os.Getenv("HEX_CANDIDATE_TEST_CRASH") == stage {
		os.Exit(86)
	}
}

func candidateFaultPartialResponse(f *os.File, name string, raw []byte) {
	if (name == "accepted.intent.json" && os.Getenv("HEX_CANDIDATE_TEST_CRASH") == "partial_destination_intent") || (name == "accepted.complete.json" && os.Getenv("HEX_CANDIDATE_TEST_CRASH") == "partial_destination_receipt") {
		_, _ = f.Write(raw[:len(raw)/2])
		_ = f.Sync()
		os.Exit(86)
	}
	if name == "prepared-manifest.json" && os.Getenv("HEX_CANDIDATE_TEST_CRASH") == "partial_prepare_manifest" {
		_, _ = f.Write(raw[:len(raw)/2])
		_ = f.Sync()
		os.Exit(86)
	}
	if name == "candidate-install-plan.json" && os.Getenv("HEX_CANDIDATE_TEST_CRASH") == "partial_install_plan_write" {
		_, _ = f.Write(raw[:len(raw)/2])
		_ = f.Sync()
		os.Exit(86)
	}
	if name == "candidate-response.json" && os.Getenv("HEX_CANDIDATE_TEST_CRASH") == "partial_response_write" {
		_, _ = f.Write(raw[:len(raw)/2])
		_ = f.Sync()
		os.Exit(86)
	}
}
