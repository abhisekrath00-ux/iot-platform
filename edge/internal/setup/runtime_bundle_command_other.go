//go:build !linux

package setup

import (
	"context"
	"fmt"
	"io"
)

func RunPrivateRuntimeAssembly(context.Context, io.Reader, io.Writer) error {
	return fmt.Errorf("private runtime assembly unavailable on this platform")
}
