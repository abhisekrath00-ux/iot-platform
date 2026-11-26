//go:build !linux

package setup

import (
	"fmt"
	"io"
)

func RunCommissioningImport(input io.Reader, output io.Writer) error {
	return fmt.Errorf("private commissioning import is Linux-only and unavailable on this platform")
}
