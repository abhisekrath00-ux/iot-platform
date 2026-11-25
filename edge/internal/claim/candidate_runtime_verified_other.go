//go:build !linux

package claim

import (
	"fmt"
	"os"
)

func candidateReadPreparedBinary(*candidateStore) ([]byte, error) {
	return nil, fmt.Errorf("private runtime assembly unavailable on this platform")
}
func candidateVerifyAcceptedRuntime(*candidateStore, string, string, map[string][]byte) error {
	return fmt.Errorf("private runtime assembly unavailable on this platform")
}

func candidateOpenRuntimeState(string) (*os.File, error) {
	return nil, fmt.Errorf("private runtime assembly unavailable")
}
func candidateCheckRuntimeState(*os.File, string) error {
	return fmt.Errorf("private runtime assembly unavailable")
}
