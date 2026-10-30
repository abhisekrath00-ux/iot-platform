package main

import (
	"context"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
	"net/http"
	"time"
)

// readinessHandler exposes no dependency addresses, credentials or raw errors.
// Checks must honor the context; one deadline bounds the complete probe.
func readinessHandler(checks ...func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		for _, check := range checks {
			if err := check(ctx); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.Write([]byte("ok"))
	}
}

func stateStoreReadiness(client *redisx.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		reply, err := client.CmdContext(ctx, "PING")
		if err != nil {
			return err
		}
		if reply != "PONG" {
			return fmt.Errorf("unexpected state-store reply")
		}
		return nil
	}
}
