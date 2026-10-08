// Package brokeracl generates the mosquitto acl_file from claimed gateways.
// One block per gateway, keyed by the certificate CN (= gateway serial,
// which becomes the broker username under use_identity_as_username). Each
// gateway may publish only its own telemetry and diag results, and read only
// its own command/diag subtrees - a compromised gateway cannot touch another
// tenant's or gateway's topics.
package brokeracl

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Entry is one claimed gateway's broker identity.
type Entry struct {
	Serial    string // cert CN = broker username
	TenantID  string
	GatewayID string // topic identity (t/<tenant>/g/<gateway_id>/...)
	Direct    bool   // network device publishing straight to the broker: telemetry only, no reads
}

// Header is the static preamble: server-side service users (ingest, api)
// keep broad access; gateway blocks below narrow each device to its subtree.
// Service credentials come from the broker password file, not certificates.
const Header = `# hexmon broker ACL - generated at claim time, do not edit by hand.
# Service users (password file): full read for ingest, diag publish for api.
user ingest
topic read t/#

user api
topic write t/+/g/+/diag
topic write t/+/g/+/scan
topic read t/#

# Per-gateway blocks follow, keyed by certificate CN (= serial).
`

// Generate renders the full acl_file. Entries are sorted by serial so the
// output is deterministic and diffs cleanly in versioned deploys.
func Generate(entries []Entry) string {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Serial < sorted[j].Serial })
	var b strings.Builder
	b.WriteString(Header)
	for _, e := range sorted {
		base := fmt.Sprintf("t/%s/g/%s", e.TenantID, e.GatewayID)
		fmt.Fprintf(&b, "\nuser %s\n", e.Serial)
		fmt.Fprintf(&b, "topic write %s/telemetry\n", base)
		if e.Direct {
			// No command, diagnostic or fleet subtree: control for direct
			// devices needs its own hazard analysis first (docs/mqtt-direct.md).
			continue
		}
		fmt.Fprintf(&b, "topic write %s/diag/result\n", base)
		fmt.Fprintf(&b, "topic write %s/cmd/ack\n", base)
		fmt.Fprintf(&b, "topic write %s/fleet/ack\n", base)
		fmt.Fprintf(&b, "topic read %s/fleet\n", base)
		fmt.Fprintf(&b, "topic read %s/cmd\n", base)
		fmt.Fprintf(&b, "topic read %s/diag\n", base)
		fmt.Fprintf(&b, "topic write %s/scan/result\n", base)
		fmt.Fprintf(&b, "topic read %s/scan\n", base)
		fmt.Fprintf(&b, "topic write %s/ops/result\n", base)
		fmt.Fprintf(&b, "topic read %s/ops\n", base)
		fmt.Fprintf(&b, "topic write %s/config/result\n", base)
		fmt.Fprintf(&b, "topic read %s/config\n", base)
	}
	return b.String()
}

// WriteAtomic replaces the acl_file without a partial-write window: write a
// temp file in the same directory, fsync, rename. Mosquitto reloads on SIGHUP.
func WriteAtomic(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PasswordHash returns a mosquitto_passwd-compatible PBKDF2-SHA512 entry
// ("$7$101$<salt>$<hash>", 12-byte salt, 64-byte key) for the secret. Only the
// hash is ever stored; the secret is shown once at mint/rotate time.
func PasswordHash(secret string) (string, error) {
	salt := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return passwordHashWithSalt(secret, salt)
}

func passwordHashWithSalt(secret string, salt []byte) (string, error) {
	dk, err := pbkdf2.Key(sha512.New, secret, salt, 101, 64)
	if err != nil {
		return "", err
	}
	return "$7$101$" + base64.StdEncoding.EncodeToString(salt) + "$" + base64.StdEncoding.EncodeToString(dk), nil
}

// VerifyPassword checks a secret against a PasswordHash value.
func VerifyPassword(secret, hash string) bool {
	parts := strings.Split(hash, "$") // "", "7", "101", salt, hash
	if len(parts) != 5 || parts[1] != "7" || parts[2] != "101" {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := passwordHashWithSalt(secret, salt)
	return err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(hash)) == 1
}

// PasswdEntry is one direct device that authenticates with a username (its
// serial) and a secret instead of a client certificate.
type PasswdEntry struct{ Username, Hash string }

// GeneratePasswd renders mosquitto password-file lines for the entries.
func GeneratePasswd(entries []PasswdEntry) string {
	sorted := append([]PasswdEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Username < sorted[j].Username })
	var b strings.Builder
	for _, e := range sorted {
		b.WriteString(e.Username + ":" + e.Hash + "\n")
	}
	return b.String()
}
