package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrSignature = errors.New("staged admission receipt is unauthenticated or outside pinned scope")
var ErrMissing = errors.New("staged retained row absent; no new deletion evidence")

const receiptDomain = "HexThings/processing-v1/admission-receipt/ed25519/v1\x00"

// ReceiptTrust is explicit pinned configuration, NOT auto-discovery or a key
// received alongside a receipt. The same key/audience is restricted to ONE
// tenant/gateway here. No runtime pairing/provisioning path constructs it.
// Key trust, rotation, revocation and rollback policy are separate requirements.
// Callers must not mutate PublicKey concurrently with verification.
type ReceiptTrust struct {
	Enabled                       bool
	Audience, TenantID, GatewayID string
	PublicKey                     ed25519.PublicKey
}
type signedPayload struct {
	Version  int     `json:"version"`
	KeyID    string  `json:"key_id"`
	Audience string  `json:"audience"`
	Receipt  Receipt `json:"receipt"`
}
type signedWire struct {
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"signature"`
}

func validAudience(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r <= 32 || r == 127 }) < 0
}

// verifyReceipt accepts ONLY the exact canonical version1 encoding signed by the
// independently pinned key. Remarshal equality refuses duplicate/unknown/case-
// folded keys, alternate encodings and trailing data, before typed matching.
// A signature does not establish trust by itself or prove downstream effects.
func verifyReceipt(wire []byte, c ReceiptTrust) (Receipt, error) {
	if !c.Enabled || !validAudience(c.Audience) || !validID(c.TenantID) || !validID(c.GatewayID) || len(c.PublicKey) != ed25519.PublicKeySize || len(wire) == 0 || len(wire) > 8192 || !utf8.Valid(wire) {
		return Receipt{}, ErrSignature
	}
	key := append(ed25519.PublicKey(nil), c.PublicKey...)
	var w signedWire
	if json.Unmarshal(wire, &w) != nil || len(w.Signature) != ed25519.SignatureSize {
		return Receipt{}, ErrSignature
	}
	canonical, err := json.Marshal(w)
	if err != nil || !bytes.Equal(canonical, wire) {
		return Receipt{}, ErrSignature
	}
	if !ed25519.Verify(key, append([]byte(receiptDomain), w.Payload...), w.Signature) {
		return Receipt{}, ErrSignature
	}
	var p signedPayload
	if json.Unmarshal(w.Payload, &p) != nil {
		return Receipt{}, ErrSignature
	}
	canonical, err = json.Marshal(p)
	if err != nil || !bytes.Equal(canonical, w.Payload) {
		return Receipt{}, ErrSignature
	}
	sum := sha256.Sum256(key)
	if p.Version != 1 || p.KeyID != hex.EncodeToString(sum[:]) || p.Audience != c.Audience || p.Receipt.TenantID != c.TenantID || p.Receipt.GatewayID != c.GatewayID {
		return Receipt{}, ErrSignature
	}
	return p.Receipt, nil
}

// ReconcileSigned atomically rereads, verifies, fully matches and deletes exactly
// one staged retained row. The receipt never supplies its own key/trust scope.
// Every failure returns false with the row retained, except a commit error is
// UNCERTAIN: false does not claim rollback. Inspect/reconcile the SAME identity,
// never create a new event or retry any external effect. ErrMissing is not new
// acknowledgment evidence. Successful deletion means server admission ONLY,
// not rule execution, history, notification/provider delivery or recipient read.
// No caller in production uses this API. Legacy queue.Queue is unchanged.
func (s *Store) ReconcileSigned(ctx context.Context, sequence int64, wire []byte, trust ReceiptTrust) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrDisabled
	}
	if ctx == nil || sequence < 1 {
		return false, ErrInput
	}
	r, err := verifyReceipt(wire, trust)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var it Item
	var event, ts string
	err = tx.QueryRowContext(ctx, `SELECT sequence,event_id,observed_at,topic,payload,digest FROM admission_inputs WHERE sequence=?`, sequence).Scan(&it.Sequence, &event, &ts, &it.Topic, &it.Payload, &it.InputSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrMissing
	}
	if err != nil {
		return false, err
	}
	if json.Unmarshal(it.Payload, &it.Reading) != nil || event != it.Reading.EventID || ts != it.Reading.ObservedAt.Format(time.RFC3339Nano) || MatchReceipt(it, r) != nil {
		return false, ErrEvidence
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM admission_inputs WHERE sequence=? AND event_id=? AND observed_at=? AND topic=? AND payload=? AND digest=?`, sequence, event, ts, it.Topic, it.Payload, it.InputSHA256)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
