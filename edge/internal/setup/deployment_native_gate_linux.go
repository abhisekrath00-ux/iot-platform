//go:build linux && hex_deployment_native_acceptance

package setup

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Acceptance-only build. Never compiled into ordinary product binaries.
func RunNativeStoppedDeploymentAcceptance(ctx context.Context, input io.Reader) error {
	if os.Geteuid() != 0 || os.Getenv("HEX_NATIVE_STOPPED_ACCEPTANCE") != "1" {
		return fmt.Errorf("explicit disposable root acceptance only")
	}
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded acceptance input required")
	}
	return recoverRootDeployment(ctx, raw)
}
