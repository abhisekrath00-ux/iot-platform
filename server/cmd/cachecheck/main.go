// cachecheck is an explicit operator probe, never a readiness or startup write.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
)

func run() error {
	raw := os.Getenv("REDIS_URL")
	opt := redisx.TLSOptions{CAFile: os.Getenv("REDIS_TLS_CA_FILE"), CertFile: os.Getenv("REDIS_TLS_CERT_FILE"), KeyFile: os.Getenv("REDIS_TLS_KEY_FILE")}
	if !strings.HasPrefix(raw, "rediss://") || opt.CAFile == "" || opt.CertFile == "" || opt.KeyFile == "" {
		return fmt.Errorf("cachecheck requires rediss:// and CA, client certificate and key files")
	}
	c, err := redisx.FromURL(raw, opt)
	if err != nil {
		return fmt.Errorf("cachecheck configuration rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.CheckCachePermissions(ctx)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Cache command probe passed. Scratch keys expire within 30 seconds of creation; not an HA/failover check.")
}
