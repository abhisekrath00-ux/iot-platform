// Package claim redeems a one-time enrollment claim code against the
// control plane and persists the resulting gateway identity locally.
package claim

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Identity struct {
	GatewayID   string `json:"gateway_id"`
	IngestToken string `json:"ingest_token"`
	// Present when the server signed the CSR sent with the claim.
	ClientCertPEM string `json:"client_cert_pem,omitempty"`
	CACertPEM     string `json:"ca_cert_pem,omitempty"`
	// Generated locally during Redeem; never sent anywhere. Not serialized
	// into identity.json - SaveIdentity writes it to identity.key instead.
	ClientKeyPEM []byte `json:"-"`
}

// ErrRejected means a server answered and refused the claim. Another address would
// reach the same control plane, so RedeemAny stops instead of trying the rest.
var ErrRejected = errors.New("claim rejected")

// RedeemAny tries each address in order. It moves on only when an address could not
// be reached (network error or a 5xx answer); a definite refusal stops the loop. It
// returns the identity and the address that worked.
func RedeemAny(ctx context.Context, urls []string, code, serial string) (*Identity, string, error) {
	var errs []string
	for _, u := range urls {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u == "" {
			continue
		}
		id, err := Redeem(ctx, u, code, serial)
		if err == nil {
			return id, u, nil
		}
		if errors.Is(err, ErrRejected) {
			return nil, u, err
		}
		errs = append(errs, u+": "+err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return nil, "", fmt.Errorf("no server address given")
	}
	return nil, "", fmt.Errorf("no server address answered: %s", strings.Join(errs, "; "))
}

// Redeem exchanges claim code + serial for a gateway identity.
// The API returns the same 403 for unknown code and serial mismatch,
// so callers get one generic error either way.
func Redeem(ctx context.Context, apiURL, code, serial string) (*Identity, error) {
	// The gateway generates its own key and sends only a CSR: the private key
	// never leaves the device.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("keygen: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: serial},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("csr: %w", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	localKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	body, _ := json.Marshal(map[string]string{
		"claim_code": code, "serial": serial,
		"csr_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
	})
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
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("server error (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w (status %d): check the code, serial, and expiry", ErrRejected, resp.StatusCode)
	}
	var out struct {
		GatewayID     string `json:"gateway_id"`
		IngestToken   string `json:"ingest_token"`
		ClientCertPEM string `json:"client_cert_pem"`
		CACertPEM     string `json:"ca_cert_pem"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.GatewayID == "" || out.IngestToken == "" {
		return nil, fmt.Errorf("claim response malformed")
	}
	return &Identity{GatewayID: out.GatewayID, IngestToken: out.IngestToken,
		ClientCertPEM: out.ClientCertPEM, CACertPEM: out.CACertPEM, ClientKeyPEM: localKeyPEM}, nil
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
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	if len(id.ClientKeyPEM) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "identity.key"), id.ClientKeyPEM, 0o600); err != nil {
			return err
		}
	}
	// PEM files the MQTT client loads directly, so a freshly claimed gateway
	// connects over mTLS with no hand-edited paths.
	if id.ClientCertPEM != "" {
		if err := os.WriteFile(filepath.Join(dir, "identity.crt"), []byte(id.ClientCertPEM), 0o644); err != nil {
			return err
		}
	}
	if id.CACertPEM != "" {
		if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte(id.CACertPEM), 0o644); err != nil {
			return err
		}
	}
	return nil
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
