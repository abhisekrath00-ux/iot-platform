package main

import (
	"context"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationApplyNewInstallPreflight(t *testing.T) {
	for _, mode := range []string{"empty", "missing-guard", "unbound", "missing-marker", "retained-intent", "unknown-file", "sent", "applied", "confirmed", "rejected", "rolled_back", "failed", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s, _, tenant, _ := applyGuardFixture(t)
			switch mode {
			case "sent", "applied", "confirmed", "rejected", "rolled_back", "failed", "expired":
				if err := insertApplyGuardRow(t, s, tenant, "preflight-history", mode); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "missing-guard" && mode != "failed" && mode != "expired" {
				if err := installApplyGuard(t, s); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			id, err := applyadmission.ProvisionFence(dir)
			if err != nil {
				t.Fatal(err)
			}
			f, err := applyadmission.OpenBoundFence(dir, id)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unbound":
				f, err = applyadmission.OpenFence(dir)
			case "missing-marker":
				err = os.Remove(filepath.Join(dir, "store.identity"))
			case "retained-intent":
				err = f.ClaimNext(tenant, tenant+"-gw", "retained", "digest", 1, nil)
			case "unknown-file":
				err = os.WriteFile(filepath.Join(dir, "unknown"), []byte("not an accepted store"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.st.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			err = applyadmission.PreflightNewInstall(t.Context(), tx, f)
			if (err == nil) != (mode == "empty") {
				t.Fatalf("mode %s result %v", mode, err)
			}
			after, readErr := os.ReadDir(dir)
			if readErr != nil || len(before) != len(after) {
				t.Fatal("preflight mutated store", readErr)
			}
			var n int
			if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM gateway_config_pushes").Scan(&n); err != nil {
				t.Fatal(err)
			}
			want := 0
			switch mode {
			case "sent", "applied", "confirmed", "rejected", "rolled_back", "failed", "expired":
				want = 1
			}
			if n != want {
				t.Fatal("preflight mutated DB", n)
			}
		})
	}
}
