//go:build !hex_candidate_fault_acceptance

package claim

import "os"

func candidateFaultCheckpoint(string)                        {}
func candidateFaultPartialResponse(*os.File, string, []byte) {}
