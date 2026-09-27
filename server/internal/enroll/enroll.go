// Package enroll implements one-time gateway claim codes.
// A claim code is shown to the installer exactly once; only its
// SHA-256 hash is stored, and redemption is single-use.
package enroll

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrExpired     = errors.New("claim code expired")
	ErrAlreadyUsed = errors.New("claim code already redeemed")
	ErrMismatch    = errors.New("claim code does not match this serial")
)

var b32 = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding) // no 0,1,I,O

// NewCode returns a fresh claim code in unambiguous groups (no 0/O/1/I),
// e.g. "ABCD-EFGH-JKLM-NPQR". 160 bits of entropy.
func NewCode() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	raw := strings.ToUpper(b32.EncodeToString(buf))
	var b strings.Builder
	for i, c := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(c)
	}
	return b.String(), nil
}

// Normalize strips separators and case so "abcd-efgh" matches "ABCDEFGH".
func Normalize(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

// Hash returns the SHA-256 of the normalized code. Only hashes are stored.
func Hash(code string) []byte {
	sum := sha256.Sum256([]byte(Normalize(code)))
	return sum[:]
}

// MatchHash compares a presented code against a stored hash in constant time.
func MatchHash(code string, stored []byte) bool {
	return subtle.ConstantTimeCompare(Hash(code), stored) == 1
}

// CheckRedeemable validates a token row before redemption.
func CheckRedeemable(expiresAt time.Time, claimedAt *time.Time, now time.Time) error {
	if claimedAt != nil {
		return ErrAlreadyUsed
	}
	if now.After(expiresAt) {
		return ErrExpired
	}
	return nil
}
