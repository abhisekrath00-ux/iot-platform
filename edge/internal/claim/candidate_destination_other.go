//go:build !linux

package claim

import "fmt"

func candidateAcceptDestination(string, string, string, map[string][]byte) error {
	return fmt.Errorf("native destination acceptance unavailable")
}
