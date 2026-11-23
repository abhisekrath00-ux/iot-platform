package applyadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Fence is an isolated immutable external-attempt journal, NOT a general retry
// log. Its directory must be retained independently of database restoration.
// No startup/menu/route creates one. There is deliberately no clear operation.
// A stale or lost copy of BOTH database and fence is not safe recovery.
type Fence struct{ directory string }

// OpenFence requires an existing private directory. A missing directory is not
// interpreted as a new empty journal and is never created automatically.
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
	return &Fence{directory: directory}, nil
}

// Claim precedes the attempt transaction commit and external publication. A
// failed write/sync/close is UNKNOWN and never removes the file or retries.
// O_EXCL provides one local shared-filesystem winner across API processes.
// These guarantees do not cover independent directories, NFS semantics or an
// administrator replacing the directory; those are deployment acceptance gates.
func (f *Fence) Claim(tenant, gateway, attempt, digest string) error {
	if f == nil || tenant == "" || gateway == "" || attempt == "" || digest == "" {
		return fmt.Errorf("retained Apply fence required")
	}
	if _, err := OpenFence(f.directory); err != nil {
		return err
	}
	path := f.gatewayPath(tenant, gateway)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("Apply is quarantined by retained attempt fence: %w", err)
	}
	// Never expose the approved configuration or credentials in this journal.
	value := struct {
		Version int       `json:"version"`
		Tenant  string    `json:"tenant"`
		Gateway string    `json:"gateway"`
		Attempt string    `json:"attempt"`
		Digest  string    `json:"sha256"`
		Created time.Time `json:"created"`
	}{1, tenant, gateway, attempt, digest, time.Now().UTC()}
	b, err := json.Marshal(value)
	if err == nil {
		var n int
		n, err = file.Write(append(b, '\n'))
		if err == nil && n != len(b)+1 {
			err = fmt.Errorf("short fence write")
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
		return fmt.Errorf("Apply fence persistence unconfirmed; keep quarantined: %w", err)
	}
	directory, err := os.Open(f.directory)
	if err != nil {
		return fmt.Errorf("Apply fence directory persistence unconfirmed: %w", err)
	}
	err = directory.Sync()
	closeErr = directory.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("Apply fence directory persistence unconfirmed: %w", err)
	}
	return nil
}

// Occupied is read-only and never interprets a corrupt/partial claim as absent.
// It does not infer broker delivery, gateway state or permission to clear it.
func (f *Fence) Occupied(tenant, gateway string) (bool, error) {
	if f == nil || tenant == "" || gateway == "" {
		return false, fmt.Errorf("retained Apply fence required")
	}
	if _, err := OpenFence(f.directory); err != nil {
		return false, err
	}
	_, err := os.Lstat(f.gatewayPath(tenant, gateway))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("retained Apply fence status unavailable: %w", err)
	}
	return true, nil
}

func (f *Fence) gatewayPath(tenant, gateway string) string {
	key := sha256.Sum256([]byte(tenant + "\x00" + gateway))
	return filepath.Join(f.directory, hex.EncodeToString(key[:])+".json")
}
