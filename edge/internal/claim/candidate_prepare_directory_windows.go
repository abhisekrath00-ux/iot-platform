//go:build windows

package claim

import "fmt"

// Preserve the published h204 store unchanged and keep preparation refused.
func (*candidateStore) MakePrivateDirectory(string) error {
	return fmt.Errorf("native Windows private preparation unavailable")
}
