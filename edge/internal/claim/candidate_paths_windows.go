//go:build windows

package claim

import "fmt"

// No Windows ACL/reparse/durability proof exists for this experimental client.
// Refuse rather than silently treating Unix modes as a Windows privacy check.
func candidatePrivateParent(string) error {
	return fmt.Errorf("candidate Windows private-path checks not implemented; no request allowed")
}
func candidatePrivateLeaf(string, ...string) error {
	return fmt.Errorf("candidate Windows retained-path checks not implemented; no request allowed")
}
