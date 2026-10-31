package main

import (
	"strings"
	"testing"
)

func TestConfigurationGate(t *testing.T) {
	for _, raw := range []string{"", "redis://user:secret@localhost:6379/2", "rediss://user:secret@localhost:6379/2"} {
		t.Run(raw[:min(len(raw), 8)], func(t *testing.T) {
			t.Setenv("REDIS_URL", raw)
			t.Setenv("REDIS_TLS_CA_FILE", "")
			t.Setenv("REDIS_TLS_CERT_FILE", "")
			t.Setenv("REDIS_TLS_KEY_FILE", "")
			if e := run(); e == nil || strings.Contains(e.Error(), "secret") {
				t.Fatal("configuration not safely gated", e)
			}
		})
	}
}
