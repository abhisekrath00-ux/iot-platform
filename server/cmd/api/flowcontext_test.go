package main

import (
	"sync"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func TestIntegrationFlowContext(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-fc")
	seed(t, s, "itest-fc2")
	ctx := t.Context()
	clean := func() { s.st.Pool.Exec(ctx, `DELETE FROM flow_context WHERE tenant_id IN ('itest-fc','itest-fc2')`) }
	clean()
	t.Cleanup(clean)
	a := &flow.PGContext{Pool: s.st.Pool, Tenant: "itest-fc"}
	b := &flow.PGContext{Pool: s.st.Pool, Tenant: "itest-fc2"}
	if _, ok, _ := a.Get("global", "n"); ok {
		t.Fatal("unset key found")
	}
	// 20 parallel increments must not lose an update
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Incr("global", "n", 1) }()
	}
	wg.Wait()
	if v, ok, err := a.Get("global", "n"); err != nil || !ok || v != 20 {
		t.Fatalf("incr total %v %v %v", v, ok, err)
	}
	if _, ok, _ := b.Get("global", "n"); ok {
		t.Fatal("another tenant sees the key")
	}
	if err := a.Set("flow/x", "n", 7); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := a.Get("flow/x", "n"); v != 7 {
		t.Fatalf("flow scope %v", v)
	}
	if v, _, _ := a.Get("global", "n"); v != 20 {
		t.Fatal("scopes are not separate")
	}
	for i := 0; i < 100; i++ {
		a.Set("flow/cap", "k"+string(rune('a'+i%26))+string(rune('a'+i/26)), 1)
	}
	if err := a.Set("flow/cap", "overflow", 1); err == nil {
		t.Fatal("key cap not enforced")
	}
	if err := a.Set("flow/cap", "kaa", 5); err != nil {
		t.Fatalf("updating an existing key at the cap must work: %v", err)
	}
}
