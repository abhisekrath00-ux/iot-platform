//go:build linux && !hex_candidate_fault_acceptance

package setup

import "os"

func runtimeBundleCheckpoint(string)                {}
func runtimeBundlePartial(*os.File, string, []byte) {}
