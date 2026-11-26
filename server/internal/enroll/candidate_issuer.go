package enroll

// CandidateIssuer is explicit construction only, never configured by globals or
// registered in production. All three trust materials must be provided by the
// embedding caller. A missing CA cannot fall back to token-only enrollment.
import (
	"crypto/ed25519"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/golang-jwt/jwt/v5"
	"time"
)

type CandidateIssuer struct {
	CA, CAKey, TokenKey []byte
	ResponseKey         ed25519.PrivateKey
	Lifetime            time.Duration
}

func (i CandidateIssuer) Issue(b AttemptBinding) (CandidateIssued, error) {
	if e := i.Validate(); e != nil {
		return CandidateIssued{}, e
	}
	if len(i.TokenKey) < 32 || len(i.ResponseKey) != ed25519.PrivateKeySize || i.Lifetime < time.Minute || i.Lifetime > 50*time.Minute || b.ID == "" || b.Tenant == "" || b.Gateway == "" || b.Serial == "" {
		return CandidateIssued{}, fmt.Errorf("explicit candidate trust and bounded identity scope required")
	}
	cert, fp, e := pki.SignCSR(i.CA, i.CAKey, []byte(b.CSR), b.Serial, 1)
	if e != nil {
		return CandidateIssued{}, e
	}
	expiry := time.Now().Add(i.Lifetime)
	token, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: b.Tenant, Role: "gateway", RegisteredClaims: jwt.RegisteredClaims{Subject: b.Gateway, ExpiresAt: jwt.NewNumericDate(expiry)}}).SignedString(i.TokenKey)
	if e != nil {
		return CandidateIssued{}, e
	}
	raw, e := CandidateSignedResponse(i.ResponseKey, b, string(cert), string(i.CA), token, expiry)
	if e != nil {
		return CandidateIssued{}, e
	}
	issued := CandidateIssued{Response: raw, Fingerprint: fp}
	if e = i.ValidateIssued(b, issued); e != nil {
		return CandidateIssued{}, e
	}
	return issued, nil
}
