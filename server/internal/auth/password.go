package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Local passwords for deployments with no identity provider (air-gapped sites). PBKDF2-HMAC-SHA256 from the
// standard library (no third-party code), a random 16-byte salt, and the iteration count stored in the hash so
// it can be raised later. Format: pbkdf2-sha256$<iterations>$<salt b64>$<hash b64>.
const (
	pbkdf2Iterations = 600000 // OWASP 2023 guidance for PBKDF2-HMAC-SHA256
	MinPasswordLen   = 12
	MaxPasswordLen   = 128
)

// The documented first-run login seeded by the installer on a fresh install. It is public knowledge, so the server
// forces it to be replaced before anything else works, and neither value may be chosen again.
const (
	DefaultAdminEmail    = "admin@hexthings.com"
	DefaultAdminPassword = "Hex@2026"
)

var ErrWeakPassword = errors.New("password must be 12 to 128 characters and not your email address")

// CheckPasswordPolicy enforces length and the obvious bad choice. It does not try to guess entropy.
func CheckPasswordPolicy(pw, email string) error {
	if len(pw) < MinPasswordLen || len(pw) > MaxPasswordLen || strings.EqualFold(pw, email) {
		return ErrWeakPassword
	}
	return nil
}

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return hashWith(pw, salt, pbkdf2Iterations)
}

func hashWith(pw string, salt []byte, iter int) (string, error) {
	dk, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(dk)), nil
}

// VerifyPassword compares in constant time. A malformed stored hash never verifies.
func VerifyPassword(pw, stored string) bool {
	p := strings.Split(stored, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil || iter < 100000 || iter > 5000000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(p[2])
	if err != nil || len(salt) < 8 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(p[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// DummyVerify burns about the same time as a real check, so an unknown email is not faster than a wrong password.
func DummyVerify(pw string) {
	VerifyPassword(pw, "pbkdf2-sha256$600000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
}
