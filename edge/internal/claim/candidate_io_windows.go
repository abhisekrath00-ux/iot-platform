//go:build windows

package claim

import "fmt"

func candidateSyncDirectory(string) error {
	return fmt.Errorf("candidate Windows durable directories unavailable")
}
func candidateRead(string, string) ([]byte, error) {
	return nil, fmt.Errorf("candidate Windows private handle reads unavailable")
}

func candidateWrite(string, string, []byte) error {
	return fmt.Errorf("candidate Windows private writes unavailable")
}
