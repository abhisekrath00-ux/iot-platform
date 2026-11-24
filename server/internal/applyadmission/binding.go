package applyadmission

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DeploymentBinding is independent operator-controlled expected identity, not
// authority to activate Apply. Retain it outside both DB backups and the store.
// Copying/rolling back both binding and store remains outside this boundary.
type DeploymentBinding struct {
	Format     int    `json:"format"`
	Deployment string `json:"deployment"`
	StoreID    string `json:"store_id"`
}

func bindingLocation(directory, path, deployment string) error {
	if deployment == "" || len(deployment) > 128 || strings.TrimSpace(deployment) != deployment || strings.ContainsAny(deployment, "\x00\r\n") {
		return fmt.Errorf("explicit deployment identity required")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("absolute independent binding file required")
	}
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return fmt.Errorf("binding parent unavailable or redirected")
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("private independent binding directory required")
	}
	rel, err := filepath.Rel(filepath.Clean(directory), path)
	if err != nil || rel == "." || !(rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return fmt.Errorf("binding must be outside retained store")
	}
	return nil
}

// PrepareNewDeploymentBinding is explicit OFFLINE preparation only, never
// startup. Caller must separately establish empty DB history/writer quiescence.
// Existing bindings/stores are never overwritten. An error after provisioning
// means uncertain preparation: preserve both locations, never reset/retry.
func PrepareNewDeploymentBinding(directory, path, deployment string) error {
	if _, err := OpenFence(directory); err != nil {
		return err
	}
	if err := bindingLocation(directory, path, deployment); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// Reserve binding before any store change; preserve even an empty reservation
	// after uncertainty so a second invocation cannot create a replacement ID.
	defer file.Close()
	if err = file.Sync(); err != nil {
		return err
	}
	reservationParent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = reservationParent.Sync()
	reservationParent.Close()
	if err != nil {
		return err
	}
	id, err := ProvisionFence(directory)
	if err != nil {
		return fmt.Errorf("preparation incomplete; preserve binding/store: %w", err)
	}
	b, err := json.Marshal(DeploymentBinding{1, deployment, id})
	if err != nil {
		return err
	}
	if _, err = file.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

// OpenDeploymentFence checks an independently supplied deployment and binding.
// It never provisions/learns identity from the store or changes server policy.
func OpenDeploymentFence(directory, path, deployment string) (*Fence, error) {
	if err := bindingLocation(directory, path, deployment); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	b, err := readPrivate(root, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	var binding DeploymentBinding
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&binding); err != nil {
		return nil, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("invalid binding trailing content")
	}
	if binding.Format != 1 || binding.Deployment != deployment {
		return nil, fmt.Errorf("deployment binding mismatch")
	}
	return OpenBoundFence(directory, binding.StoreID)
}
