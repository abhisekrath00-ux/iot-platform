package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type brokerProof struct {
	ID          string     `json:"id"`
	Nonce       string     `json:"nonce"`
	Topic       string     `json:"topic"`
	Fingerprint string     `json:"cert_fingerprint"`
	State       string     `json:"state"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ObservedAt  *time.Time `json:"observed_at"`
}

var proofNonce = regexp.MustCompile(`^[a-f0-9]{64}$`)
var proofPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// FinishMigration sends a nonce only to the dedicated own-gateway pairing
// topic and checks the independent HTTPS receipt recorded by ingest. It never
// subscribes, issues a command, scans a port, reads a meter or starts a source.
func FinishMigration(ctx context.Context, client *http.Client, server, dir, serial string, id *claim.Identity, b MigrationBundle) error {
	if id == nil || id.GatewayID == "" || b.RequestID == "" || strings.ContainsAny(b.RequestID, "/?#\\") {
		return fmt.Errorf("current saved pairing request required")
	}
	if _, err := ValidateMigrationBundle(dir, serial, b); err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(b.ClientCert))
	if block == nil {
		return fmt.Errorf("certificate unavailable")
	}
	sum := sha256.Sum256(block.Bytes)
	fingerprint := hex.EncodeToString(sum[:])
	var proof brokerProof
	if err := migrationHTTP(ctx, client, server, id, "POST", "/v1/edge/self/identity-migrations/"+b.RequestID+"/broker-proof", nil, &proof); err != nil {
		return err
	}
	parts := strings.Split(proof.Topic, "/")
	if len(parts) != 6 || parts[0] != "t" || !proofPart.MatchString(parts[1]) || parts[2] != "g" || parts[3] != id.GatewayID || parts[4] != "pairing" || parts[5] != "proof" || !proofNonce.MatchString(proof.Nonce) || proof.Fingerprint != fingerprint || proof.ID == "" || len(proof.ID) > 64 || strings.ContainsAny(proof.ID, "/?#\\") || !time.Now().Before(proof.ExpiresAt) {
		return fmt.Errorf("invalid or stale broker proof challenge; certificate files unchanged")
	}
	return InstallMigration(ctx, dir, serial, id.GatewayID, b, func(ctx context.Context, broker mqtt.Client) error {
		payload, _ := json.Marshal(map[string]string{"id": proof.ID, "nonce": proof.Nonce})
		for {
			token := broker.Publish(proof.Topic, 1, false, payload)
			select {
			case <-token.Done():
				if token.Error() != nil {
					return fmt.Errorf("broker diagnostic proof publish failed; certificate files unchanged")
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			var receipt brokerProof
			if err := migrationHTTP(ctx, client, server, id, "GET", "/v1/edge/self/broker-proofs/"+proof.ID, nil, &receipt); err != nil {
				return err
			}
			if receipt.ID != proof.ID || receipt.Fingerprint != fingerprint || !time.Now().Before(receipt.ExpiresAt) {
				return fmt.Errorf("broker proof identity changed or expired; certificate files unchanged")
			}
			if receipt.State == "observed" && receipt.ObservedAt != nil {
				return nil
			}
			if receipt.State != "waiting_for_broker_observation" {
				return fmt.Errorf("invalid broker proof receipt; certificate files unchanged")
			}
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return fmt.Errorf("server has not observed own-gateway broker authorization; certificate files unchanged")
			}
		}
	})
}
