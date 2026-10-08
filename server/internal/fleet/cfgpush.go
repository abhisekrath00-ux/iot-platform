package fleet

import (
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// CanonicalConfigPush is the exact byte string signed for a device-template push. It is duplicated in
// edge/internal/cfgpush (Canonical); both sides test the same fixed vector.
func CanonicalConfigPush(tenant, serial string, version int, pushID, sha string, expires time.Time) []byte {
	return []byte(strings.Join([]string{"hexmon-config-push-v1", tenant, serial, strconv.Itoa(version), pushID, sha, expires.UTC().Format(time.RFC3339)}, "\n"))
}

func SignConfigPush(key ed25519.PrivateKey, tenant, serial string, version int, pushID, sha string, expires time.Time) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, CanonicalConfigPush(tenant, serial, version, pushID, sha, expires)))
}
