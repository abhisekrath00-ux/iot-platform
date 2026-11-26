package enroll

// Opt-in candidate. No production caller, migration or HTTP route registers it.
import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAttemptRefused = errors.New("claim attempt unavailable; preserve retained identity")

type AttemptInput struct {
	ID, Serial, CSR, Code string
	Signature             []byte
}
type AttemptBinding struct{ ID, Tenant, Gateway, Serial, CSR string }
type CandidateAttempts struct{ Pool *pgxpool.Pool }
type CandidateIssued struct {
	Response    []byte
	Fingerprint string
}

// AttemptProofDigest binds purpose and all public request inputs. The claim code
// is not signed/stored as plaintext. This proof is transported only over TLS.
func AttemptProofDigest(in AttemptInput, recoverOnly bool) []byte {
	op := "claim"
	if recoverOnly {
		op = "recover"
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("hexthings-claim-v1\x00%s\x00%s\x00%s\x00%s", op, in.ID, in.Serial, in.CSR)))
	return h[:]
}
func validateAttempt(in AttemptInput, recoverOnly bool) (string, error) {
	if len(in.ID) != 64 || in.Serial == "" || len(in.Serial) > 256 || len(in.CSR) > 16384 || in.Code == "" {
		return "", ErrAttemptRefused
	}
	if _, e := hex.DecodeString(in.ID); e != nil {
		return "", ErrAttemptRefused
	}
	b, rest := pem.Decode([]byte(in.CSR))
	if b == nil || b.Type != "CERTIFICATE REQUEST" || len(rest) != 0 {
		return "", ErrAttemptRefused
	}
	csr, e := x509.ParseCertificateRequest(b.Bytes)
	if e != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != in.Serial {
		return "", ErrAttemptRefused
	}
	key, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve.Params().BitSize != 256 || !ecdsa.VerifyASN1(key, AttemptProofDigest(in, recoverOnly), in.Signature) {
		return "", ErrAttemptRefused
	}
	sum := sha256.Sum256(b.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// Execute commits token consumption, gateway activation and exact response in
// one transaction. RecoverOnly never invokes issue and never redeems a token.
// A transport/commit error remains uncertain; caller may explicitly recover
// the same attempt, never infer permission for another claim.
func (c CandidateAttempts) Execute(ctx context.Context, in AttemptInput, recoverOnly bool, issue func(AttemptBinding) (CandidateIssued, error)) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	digest, e := validateAttempt(in, recoverOnly)
	if e != nil {
		return nil, e
	}
	tx, e := c.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='10s'"); e != nil {
		return nil, e
	}
	var token, tenant, gateway, serial string
	var expiry time.Time
	var claimed *time.Time
	e = tx.QueryRow(ctx, `SELECT id,tenant_id,gateway_id,serial,expires_at,claimed_at FROM enrollment_tokens WHERE code_hash=$1 FOR UPDATE`, Hash(in.Code)).Scan(&token, &tenant, &gateway, &serial, &expiry, &claimed)
	if e != nil || serial != in.Serial || !time.Now().Before(expiry) {
		return nil, ErrAttemptRefused
	}
	var oldToken, oldTenant, oldGateway, oldSerial, oldDigest, oldCSR, oldFingerprint string
	var response []byte
	var resultExpiry time.Time
	var revoked bool
	e = tx.QueryRow(ctx, `SELECT token_id,tenant_id,gateway_id,serial,csr_sha256,csr_pem,response,expires_at,revoked,fingerprint FROM enrollment_attempt_candidate WHERE attempt_id=$1`, in.ID).Scan(&oldToken, &oldTenant, &oldGateway, &oldSerial, &oldDigest, &oldCSR, &response, &resultExpiry, &revoked, &oldFingerprint)
	if e == nil {
		if oldToken != token || oldTenant != tenant || oldGateway != gateway || oldSerial != serial || oldDigest != digest || oldCSR != in.CSR || revoked || claimed == nil || !time.Now().Before(resultExpiry) {
			return nil, ErrAttemptRefused
		}
		var status, fingerprint string
		if e = tx.QueryRow(ctx, `SELECT status,COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1 AND tenant_id=$2 FOR SHARE`, gateway, tenant).Scan(&status, &fingerprint); e != nil || status != "active" || fingerprint != oldFingerprint {
			return nil, ErrAttemptRefused
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return response, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if recoverOnly || claimed != nil || issue == nil {
		return nil, ErrAttemptRefused
	}
	issued, e := issue(AttemptBinding{in.ID, tenant, gateway, serial, in.CSR})
	response = issued.Response
	if e != nil {
		return nil, e
	}
	if len(response) == 0 || len(response) > 131072 || len(issued.Fingerprint) != 64 {
		return nil, ErrAttemptRefused
	}
	// Persist only the effective recovery deadline, never the longer code TTL.
	var result struct {
		Manifest []byte `json:"manifest"`
	}
	var manifest CandidateManifest
	if json.Unmarshal(response, &result) != nil || json.Unmarshal(result.Manifest, &manifest) != nil || manifest.Expires <= time.Now().Unix() {
		return nil, ErrAttemptRefused
	}
	resultDeadline := time.Unix(manifest.Expires, 0)
	if resultDeadline.Before(expiry) {
		expiry = resultDeadline
	}
	tag, e := tx.Exec(ctx, `UPDATE enrollment_tokens SET claimed_at=now() WHERE id=$1 AND claimed_at IS NULL`, token)
	if e != nil || tag.RowsAffected() != 1 {
		return nil, ErrAttemptRefused
	}
	tag, e = tx.Exec(ctx, `UPDATE gateways SET status='active',last_seen_at=now(),cert_fingerprint=$3 WHERE id=$1 AND tenant_id=$2 AND status='pending'`, gateway, tenant, issued.Fingerprint)
	if e != nil || tag.RowsAffected() != 1 {
		return nil, ErrAttemptRefused
	}
	_, e = tx.Exec(ctx, `INSERT INTO enrollment_attempt_candidate(attempt_id,token_id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,response,expires_at,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, in.ID, token, tenant, gateway, serial, in.CSR, digest, response, expiry, issued.Fingerprint)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return response, nil
}
