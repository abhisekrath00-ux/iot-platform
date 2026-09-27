// Package claim redeems a one-time enrollment claim code against the
// control plane and persists the resulting gateway identity locally.
package claim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Identity struct {
	GatewayID   string `json:"gateway_id"`
	IngestToken string `json:"ingest_token"`
}

// Redeem exchanges claim code + serial for a gateway identity.
// The API returns the same 403 for unknown code and serial mismatch,
// so callers get one generic error either way.
func Redeem(ctx context.Context, apiURL, code, serial string) (*Identity, error) {
	body, _ := json.Marshal(map[string]string{"claim_code": code, "serial": serial})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/v1/enrollment/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claim request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claim rejected (status %d): check the code, serial, and expiry", resp.StatusCode)
	}
	var out struct {
		GatewayID   string `json:"gateway_id"`
		IngestToken string `json:"ingest_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.GatewayID == "" || out.IngestToken == "" {
		return nil, fmt.Errorf("claim response malformed")
	}
	return &Identity{GatewayID: out.GatewayID, IngestToken: out.IngestToken}, nil
}

// SaveIdentity writes the identity with owner-only permissions. It refuses to
// overwrite an existing identity: reclaiming silently would re-key a gateway.
func SaveIdentity(dir string, id *Identity) error {
	p := filepath.Join(dir, "identity.json")
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("identity already exists at %s; remove it deliberately to re-claim", p)
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// LoadIdentity reads a previously saved identity.
func LoadIdentity(dir string) (*Identity, error) {
	b, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil || id.GatewayID == "" {
		return nil, fmt.Errorf("identity file malformed")
	}
	return &id, nil
}
