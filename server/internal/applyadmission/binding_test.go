package applyadmission

import (
	"os"
	"path/filepath"
	"testing"
)

func bindingFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	os.Chmod(base, 0700)
	store := filepath.Join(base, "store")
	os.Mkdir(store, 0700)
	return store, filepath.Join(base, "expected.json")
}
func TestDeploymentBindingPrepareReopenAndUnresolved(t *testing.T) {
	store, path := bindingFixture(t)
	if err := PrepareNewDeploymentBinding(store, path, "review-deployment"); err != nil {
		t.Fatal(err)
	}
	f, err := OpenDeploymentFence(store, path, "review-deployment")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ClaimNext("t", "g", "attempt", "digest", 1, nil); err != nil {
		t.Fatal(err)
	}
	f, err = OpenDeploymentFence(store, path, "review-deployment")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ClaimNext("t", "g", "another", "digest", 1, nil); err == nil {
		t.Fatal("restart rearmed")
	}
	if err = PrepareNewDeploymentBinding(store, path, "review-deployment"); err == nil {
		t.Fatal("existing binding overwritten")
	}
	if _, err = OpenDeploymentFence(store, path, "other-deployment"); err == nil {
		t.Fatal("wrong deployment accepted")
	}
}
func TestDeploymentBindingRefusals(t *testing.T) {
	for _, mode := range []string{"inside-store", "existing-binding", "nonempty-store", "symlink-binding", "public-binding", "partial-binding", "unknown-field", "trailing-data", "wrong-store", "missing-store"} {
		t.Run(mode, func(t *testing.T) {
			store, path := bindingFixture(t)
			switch mode {
			case "inside-store":
				path = filepath.Join(store, "binding.json")
			case "existing-binding":
				os.WriteFile(path, []byte("existing"), 0600)
			case "nonempty-store":
				os.WriteFile(filepath.Join(store, "existing"), []byte("history"), 0600)
			}
			if mode == "inside-store" || mode == "existing-binding" || mode == "nonempty-store" {
				if PrepareNewDeploymentBinding(store, path, "d") == nil {
					t.Fatal("unsafe preparation allowed")
				}
				if mode == "nonempty-store" {
					if PrepareNewDeploymentBinding(store, path, "d") == nil {
						t.Fatal("uncertain preparation retried")
					}
				}
				return
			}
			if err := PrepareNewDeploymentBinding(store, path, "d"); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			switch mode {
			case "symlink-binding":
				os.Rename(path, path+".original")
				os.Symlink(path+".original", path)
			case "public-binding":
				os.Chmod(path, 0644)
			case "partial-binding":
				os.WriteFile(path, []byte("{"), 0600)
			case "unknown-field":
				os.WriteFile(path, append([]byte(`{"unknown":1,`), b[1:]...), 0600)
			case "trailing-data":
				os.WriteFile(path, append(b, []byte("{}")...), 0600)
			case "wrong-store":
				os.Remove(filepath.Join(store, "store.identity"))
				ProvisionFence(store)
			case "missing-store":
				os.RemoveAll(store)
			}
			if _, err := OpenDeploymentFence(store, path, "d"); err == nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
}
