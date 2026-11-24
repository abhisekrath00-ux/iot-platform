package main

import (
	"context"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationApplyDeploymentBindingRestart(t *testing.T) {
	s, m, tenant, path := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	os.Chmod(base, 0700)
	directory := filepath.Join(base, "retained")
	os.Mkdir(directory, 0700)
	binding := filepath.Join(base, "independent.json")
	if err := applyadmission.PrepareNewDeploymentBinding(directory, binding, "disposable-deployment"); err != nil {
		t.Fatal(err)
	}
	f, err := applyadmission.OpenDeploymentFence(directory, binding, "disposable-deployment")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.st.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = applyadmission.PreflightNewInstall(t.Context(), tx, f); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(context.Background())
	s.applySafetyCandidate = true
	s.applyAttemptFence = f
	calls := 0
	s.opsPublishHook = func(string, []byte) error { calls++; return nil }
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 202 || calls != 1 {
		t.Fatal(first.Code, calls, first.Body.String())
	}
	// Reopen the independently bound directory, do not reuse its in-memory handle.
	s.applyAttemptFence, err = applyadmission.OpenDeploymentFence(directory, binding, "disposable-deployment")
	if err != nil {
		t.Fatal(err)
	}
	second := call(m, tenant, "admin", "POST", path, `{}`)
	if second.Code != 409 || calls != 1 {
		t.Fatal("restart rearmed", second.Code, calls)
	}
	tx, err = s.st.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = applyadmission.PreflightNewInstall(t.Context(), tx, s.applyAttemptFence); err == nil {
		t.Fatal("used deployment readopted")
	}
	tx.Rollback(context.Background())
	moved := directory + "-original"
	if err = os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(directory, 0700)
	if _, err = applyadmission.OpenDeploymentFence(directory, binding, "disposable-deployment"); err == nil {
		t.Fatal("missing mounted history accepted")
	}
	if _, err = applyadmission.ProvisionFence(directory); err != nil {
		t.Fatal(err)
	}
	if _, err = applyadmission.OpenDeploymentFence(directory, binding, "disposable-deployment"); err == nil {
		t.Fatal("replacement mounted history accepted")
	}
	third := call(m, tenant, "admin", "POST", path, `{}`)
	if third.Code == 202 || calls != 1 {
		t.Fatal("replaced live store admitted publication", third.Code, calls)
	}
}
