// Package configaudit checks the API's environment for settings that are unsafe for a real deployment. It reads
// only environment values it is handed, touches no network or database, and never prints a secret.
// `api -check-config` prints the report and exits non-zero on any FAIL; at startup the findings are logged,
// and STRICT_CONFIG=1 makes any FAIL stop the process.
package configaudit

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"
)

type Level string

const (
	Fail Level = "FAIL" // unsafe for any real deployment
	Warn Level = "WARN" // acceptable only knowingly (labs, closed networks); see docs/pilot-readiness.md
)

type Finding struct {
	Level Level
	Key   string
	Msg   string
}

var weakSecrets = map[string]bool{"devsecret": true, "secret": true, "changeme": true, "password": true, "jwt-secret": true, "test": true, "dev": true}

// Audit inspects get(name) values. It is pure so it can be tested without touching the process environment.
func Audit(get func(string) string) []Finding {
	var out []Finding
	add := func(l Level, k, m string) { out = append(out, Finding{l, k, m}) }
	on := func(k string) bool { return get(k) == "1" || strings.EqualFold(get(k), "true") }

	if s := get("JWT_SIGNING_SECRET"); len(s) < 32 || weakSecrets[strings.ToLower(s)] || distinct(s) < 8 || strings.HasPrefix(strings.ToLower(s), "change-me") {
		add(Fail, "JWT_SIGNING_SECRET", "must be at least 32 random characters and not a placeholder (for example: openssl rand -base64 48)")
	}
	if k := get("SECRETS_KEY"); k == "" {
		add(Warn, "SECRETS_KEY", "not set: the encrypted per-tenant secrets store is disabled")
	} else if b, err := base64.StdEncoding.DecodeString(k); err != nil || len(b) != 32 {
		add(Fail, "SECRETS_KEY", "must be base64 of exactly 32 bytes")
	} else if strings.Count(string(b), string(b[0])) == len(b) {
		add(Fail, "SECRETS_KEY", "is a repeated single character; generate it with openssl rand -base64 32")
	}
	if db := get("DATABASE_URL"); db != "" {
		if u, err := url.Parse(db); err == nil && u.Host != "" && !isLocal(u.Hostname()) && (u.Query().Get("sslmode") == "disable" || u.Query().Get("sslmode") == "") {
			add(Warn, "DATABASE_URL", "database is remote and the connection is not forced to TLS (sslmode=require or verify-full)")
		}
	}
	if get("MQTT_TLS") == "false" {
		add(Warn, "MQTT_TLS", "broker traffic is not encrypted; use 8883 with mTLS outside a lab")
	}
	if u := get("APP_PUBLIC_URL"); u != "" {
		if pu, err := url.Parse(u); err != nil || pu.Host == "" {
			add(Fail, "APP_PUBLIC_URL", "is not a valid URL")
		} else if pu.Scheme == "http" && !isLocal(pu.Hostname()) {
			add(Warn, "APP_PUBLIC_URL", "uses plain http; sign-in and reset links would travel unencrypted")
		}
	}
	if on("SELF_SIGNUP") {
		add(Warn, "SELF_SIGNUP", "on: sign-up addresses are not verified; set SELF_SIGNUP_MAX_TENANTS and the default quota first")
		if !on("LOCAL_LOGIN") {
			add(Warn, "SELF_SIGNUP", "has no effect without LOCAL_LOGIN=1")
		}
	}
	if on("LOCAL_LOGIN") && get("OIDC_ISSUER") == "" {
		add(Warn, "LOCAL_LOGIN", "local passwords are the only sign-in; enforce MFA for admins (docs/security.md)")
	}
	for _, k := range []string{"TEST_MAIL", "SMS_GATEWAY_ALLOW_LOOPBACK"} {
		if on(k) || get(k) == "1" {
			add(Fail, k, "is a test/development switch and must not be set in a real deployment")
		}
	}
	return out
}

// distinct counts different characters, to catch "aaaa...a" and similar low-entropy placeholders.
func distinct(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen)
}

func isLocal(h string) bool {
	if h == "localhost" || strings.HasPrefix(h, "/") || h == "" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// HasFail reports whether any finding is a FAIL.
func HasFail(fs []Finding) bool {
	for _, f := range fs {
		if f.Level == Fail {
			return true
		}
	}
	return false
}

func Format(fs []Finding) string {
	if len(fs) == 0 {
		return "config check: no findings\n"
	}
	var b strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&b, "%s %s: %s\n", f.Level, f.Key, f.Msg)
	}
	return b.String()
}
