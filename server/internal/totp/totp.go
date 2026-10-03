// Package totp implements RFC 6238 time-based one-time codes (HMAC-SHA1, 6 digits, 30 s steps), the
// format every authenticator app understands. Standard library only.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const stepSeconds = 30

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns 20 random bytes.
func NewSecret() []byte {
	b := make([]byte, 20)
	rand.Read(b)
	return b
}

// Base32 is the form an authenticator app asks you to type.
func Base32(secret []byte) string { return enc.EncodeToString(secret) }

// URI is the otpauth:// link authenticator apps import.
func URI(issuer, account string, secret []byte) string {
	v := url.Values{"secret": {Base32(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

// CodeAt is the code for a 30-second step counter.
func CodeAt(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := (uint32(sum[off]&0x7f) << 24) | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", n%1000000)
}

// Step is the counter for a time.
func Step(t time.Time) int64 { return t.Unix() / stepSeconds }

// Verify accepts the current step and one either side (clock drift). It returns the matched step
// so the caller can refuse the same step twice (replay): store it and require a larger one next time.
func Verify(secret []byte, code string, now time.Time, lastUsed int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	cur := Step(now)
	for _, s := range []int64{cur - 1, cur, cur + 1} {
		if s > lastUsed && subtle.ConstantTimeCompare([]byte(CodeAt(secret, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}
