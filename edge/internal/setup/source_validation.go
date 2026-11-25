package setup

import (
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"math"
)

// validateSourceInputs runs only before local TLS paths are mapped. It never
// opens source-selected credentials or probes a device. A source requiring
// credentials must use a separate explicit local-secret provisioning flow.
func validateSourceInputs(cfg *config.Config) error {
	for _, d := range cfg.Devices {
		if d.ClientCert != "" || d.ClientKey != "" || d.ServerCert != "" || d.PasswordEnv != "" || d.CommunityEnv != "" || d.AuthPassEnv != "" || d.PrivPassEnv != "" {
			return fmt.Errorf("source cannot select local credential files or environment secrets")
		}
		if d.Interval <= 0 {
			return fmt.Errorf("source polling interval required")
		}
		for _, point := range d.Points {
			if math.IsNaN(point.Min) || math.IsNaN(point.Max) || math.IsInf(point.Min, 0) || math.IsInf(point.Max, 0) || point.Max <= point.Min {
				return fmt.Errorf("source point finite validation range required")
			}
		}
		if len(d.Writes) > 0 {
			return fmt.Errorf("device writes refused")
		}
	}
	if config.HasError(config.Lint(cfg, "linux")) {
		return fmt.Errorf("source configuration fails runtime lint")
	}
	return nil
}
