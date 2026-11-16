package ingestprocessing

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrReceiptSigning = errors.New("staged signed admission requires explicit isolated signing configuration")

const receiptDomain = "HexThings/processing-v1/admission-receipt/ed25519/v1\x00"

// ReceiptSigning is NOT runtime enrollment, a trust grant or a key store. No
// production caller configures this API. The configured audience must identify
// one deployment/trust domain, not a user-controlled incoming payload value.
// PrivateKey must be independently protected and never sent to an edge. Tests
// generate ephemeral keys only. Provisioning/rotation/revocation remain gates.
type ReceiptSigning struct {
	Enabled    bool
	Audience   string
	PrivateKey ed25519.PrivateKey
}
type signedAdmissionPayload struct {
	Version  int              `json:"version"`
	KeyID    string           `json:"key_id"`
	Audience string           `json:"audience"`
	Receipt  AdmissionReceipt `json:"receipt"`
}
type signedAdmissionWire struct {
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"signature"`
}

func validAudience(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r <= 32 || r == 127 }) < 0
}
func receiptSigningKey(c ReceiptSigning) (ed25519.PrivateKey, error) {
	if !c.Enabled || !validAudience(c.Audience) || len(c.PrivateKey) != ed25519.PrivateKeySize {
		return nil, ErrReceiptSigning
	}
	// Copy before admission so caller mutation after return cannot alter evidence.
	// As with all Go slice APIs, concurrent mutation during this call is forbidden.
	k := append(ed25519.PrivateKey(nil), c.PrivateKey...)
	// Refuse a malformed expanded key rather than signing with inconsistent halves.
	expected := ed25519.NewKeyFromSeed(k.Seed())
	if !k.Equal(expected) {
		return nil, ErrReceiptSigning
	}
	return k, nil
}

// AdmitSignedMQTT signs ONLY evidence returned by this engine's committed or
// exact-replay admission. It never signs arbitrary caller-supplied receipts.
// The caller must authenticate publishers/enforce topic ACLs as AdmitMQTT requires.
// Invalid signing configuration refuses BEFORE DB access. A signing/encoding
// failure after commit is uncertain, returns no token and requires SAME-input
// reconciliation. There is no publish, retry, ACK or derived/provider call here.
// A valid signature proves only possession of the configured key. Its authority
// still depends on independently trusted edge pairing and key/audience scope.
func (x Engine) AdmitSignedMQTT(ctx context.Context, topic string, payload []byte, c ReceiptSigning) ([]byte, error) {
	if !x.Enabled {
		return nil, ErrDisabled
	}
	if ctx == nil {
		return nil, ErrReceiptSigning
	}
	key, err := receiptSigningKey(c)
	if err != nil {
		return nil, err
	}
	r, err := x.AdmitMQTT(ctx, topic, payload)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	b, err := json.Marshal(signedAdmissionPayload{Version: 1, KeyID: hex.EncodeToString(sum[:]), Audience: c.Audience, Receipt: r})
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(key, append([]byte(receiptDomain), b...))
	return json.Marshal(signedAdmissionWire{Payload: b, Signature: signature})
}
