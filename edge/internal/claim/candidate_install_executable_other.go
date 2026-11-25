//go:build !linux

package claim

import "fmt"

func candidateReadOwnExecutable() ([]byte, error) {
	return nil, fmt.Errorf("native executable preparation unavailable on this platform")
}
