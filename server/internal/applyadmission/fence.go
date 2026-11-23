package applyadmission

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fence retains immutable intent independently of database restoration. No
// product startup opens or creates it. Never clear it to retry an Apply.
type Fence struct {
	directory  string
	identity   os.FileInfo
	expectedID string
}

// Attempt is retained intent, NOT evidence of delivery or terminal execution.
type Attempt struct {
	Format   int       `json:"format"`
	Tenant   string    `json:"tenant"`
	Gateway  string    `json:"gateway"`
	ID       string    `json:"attempt"`
	Digest   string    `json:"sha256"`
	Version  int       `json:"version"`
	Previous string    `json:"previous,omitempty"`
	Created  time.Time `json:"created"`
}

func OpenFence(directory string) (*Fence, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("absolute retained Apply fence required")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != filepath.Clean(directory) {
		return nil, fmt.Errorf("retained Apply fence unavailable or redirected")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("private retained Apply fence required")
	}
	return &Fence{directory: directory, identity: info}, nil
}
func (f *Fence) check(tenant, gateway string) error {
	if f == nil || tenant == "" || gateway == "" {
		return fmt.Errorf("retained Apply fence required")
	}
	current, err := OpenFence(f.directory)
	if err != nil {
		return err
	}
	if !os.SameFile(current.identity, f.identity) {
		return fmt.Errorf("retained Apply directory replaced")
	}
	return nil
}

// Each operation pins the original directory while accessing children. A live
// path replacement cannot redirect a claim into an empty replacement store.
func (f *Fence) root(tenant, gateway string) (*os.Root, error) {
	if err := f.check(tenant, gateway); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(f.directory)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(info, f.identity) {
		root.Close()
		return nil, fmt.Errorf("retained Apply directory replaced while opening")
	}
	if f.expectedID != "" {
		b, err := readPrivate(root, "store.identity")
		if err != nil || string(b) != f.expectedID+"\n" {
			root.Close()
			return nil, fmt.Errorf("retained Apply store identity mismatch")
		}
	}
	return root, nil
}
func readPrivate(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return nil, fmt.Errorf("retained Apply private record unavailable")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(opened, info) {
		return nil, fmt.Errorf("retained Apply record replaced")
	}
	return io.ReadAll(io.LimitReader(file, 4097))
}

// ProvisionFence is an explicit offline/test operation, NEVER startup recovery.
// The new store ID must be retained independently before admission is enabled.
// It refuses nonempty stores and never resets existing/malformed markers.
func ProvisionFence(directory string) (string, error) {
	f, err := OpenFence(directory)
	if err != nil {
		return "", err
	}
	root, err := f.root("provision", "offline")
	if err != nil {
		return "", err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil || len(entries) != 0 {
		return "", fmt.Errorf("new retained store must be empty")
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	file, err := root.OpenFile("store.identity", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = file.WriteString(id + "\n")
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = dir.Sync()
	}
	if err == nil {
		err = f.check("provision", "offline")
	}
	if err != nil {
		return "", fmt.Errorf("retained store provisioning uncertain; do not reinitialize: %w", err)
	}
	return id, nil
}

// OpenBoundFence requires an independent expected identity. It never learns or
// accepts a replacement identity from the directory that is being validated.
func OpenBoundFence(directory, expectedID string) (*Fence, error) {
	decoded, err := hex.DecodeString(expectedID)
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("independent retained store identity required")
	}
	f, err := OpenFence(directory)
	if err != nil {
		return nil, err
	}
	f.expectedID = expectedID
	root, err := f.root("open", "bound")
	if err != nil {
		return nil, err
	}
	root.Close()
	return f, nil
}

// RequireBound is used by the integrated handler; unbound directory handles
// are useful only for primitive tests and cannot authorize product admission.
func (f *Fence) RequireBound() error {
	if f == nil || f.expectedID == "" {
		return fmt.Errorf("independently bound retained Apply store required")
	}
	return nil
}

func (f *Fence) gatewayPath(tenant, gateway string) string {
	key := sha256.Sum256([]byte(tenant + "\x00" + gateway))
	return filepath.Join(f.directory, hex.EncodeToString(key[:])+".json")
}
func (f *Fence) attemptPath(tenant, gateway string, version int) string {
	base := f.gatewayPath(tenant, gateway)
	if version == 1 {
		return base
	}
	return fmt.Sprintf("%s.%010d", base, version)
}

// Latest validates the complete bounded local chain. Partial/malformed entries
// never mean empty history. No write, rename, removal or delivery inference.
func (f *Fence) Latest(tenant, gateway string) (*Attempt, error) {
	root, err := f.root(tenant, gateway)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("retained Apply directory unreadable")
	}
	base := filepath.Base(f.gatewayPath(tenant, gateway))
	count := 0
	for _, entry := range entries {
		if entry.Name() == base || strings.HasPrefix(entry.Name(), base+".") {
			count++
		}
	}
	if count > 100000 {
		return nil, fmt.Errorf("retained Apply history needs operator inspection")
	}
	var previous *Attempt
	for version := 1; version <= count; version++ {
		path := filepath.Base(f.attemptPath(tenant, gateway, version))
		info, err := root.Lstat(path)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("retained Apply history has a gap")
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
			return nil, fmt.Errorf("retained Apply chain unavailable")
		}
		b, err := readPrivate(root, path)
		if err != nil {
			return nil, fmt.Errorf("retained Apply chain unreadable")
		}
		var current Attempt
		if json.Unmarshal(b, &current) != nil || current.Format != 2 || current.Tenant != tenant || current.Gateway != gateway || current.Version != version || current.ID == "" || current.Digest == "" || current.Created.IsZero() {
			return nil, fmt.Errorf("retained Apply chain invalid")
		}
		expected := ""
		if previous != nil {
			expected = previous.ID
		}
		if current.Previous != expected {
			return nil, fmt.Errorf("retained Apply chain mismatch")
		}
		previous = &current
	}
	return previous, nil
}

// Claim is first-attempt only. Never use it to reset a history or retry.
func (f *Fence) Claim(tenant, gateway, attempt, digest string) error {
	return f.ClaimNext(tenant, gateway, attempt, digest, 1, nil)
}

// ClaimNext requires the exact prior immutable head, not permission to retry
// it. Caller must independently verify matching terminal database receipt and
// new reviewed request under its gateway transaction. No automatic retries.
func (f *Fence) ClaimNext(tenant, gateway, attempt, digest string, version int, prior *Attempt) error {
	if err := f.check(tenant, gateway); err != nil {
		return err
	}
	if attempt == "" || digest == "" || version < 1 {
		return fmt.Errorf("valid retained intent required")
	}
	latest, err := f.Latest(tenant, gateway)
	if err != nil {
		return err
	}
	if latest == nil {
		if prior != nil || version != 1 {
			return fmt.Errorf("retained history absent; recovery required")
		}
	} else if prior == nil || *prior != *latest || version != latest.Version+1 || attempt == latest.ID {
		return fmt.Errorf("retained history mismatch; keep quarantined")
	}
	value := Attempt{Format: 2, Tenant: tenant, Gateway: gateway, ID: attempt, Digest: digest, Version: version, Created: time.Now().UTC()}
	if latest != nil {
		value.Previous = latest.ID
	}
	root, err := f.root(tenant, gateway)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(f.attemptPath(tenant, gateway, version)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("Apply quarantined by retained intent: %w", err)
	}
	b, err := json.Marshal(value)
	if err == nil {
		var n int
		n, err = file.Write(append(b, '\n'))
		if err == nil && n != len(b)+1 {
			err = fmt.Errorf("short intent write")
		}
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("Apply intent persistence unconfirmed: %w", err)
	}
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("Apply directory persistence unconfirmed: %w", err)
	}
	err = directory.Sync()
	closeErr = directory.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("Apply directory persistence unconfirmed: %w", err)
	}
	return f.check(tenant, gateway)
}

// Occupied does not parse content: a partial file is still occupied. It never
// grants progression or interprets an invalid intent as no prior attempt.
func (f *Fence) Occupied(tenant, gateway string) (bool, error) {
	root, err := f.root(tenant, gateway)
	if err != nil {
		return false, err
	}
	defer root.Close()
	_, err = root.Lstat(filepath.Base(f.gatewayPath(tenant, gateway)))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
