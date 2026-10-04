package configaudit

import (
	"encoding/base64"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func find(fs []Finding, key string, l Level) bool {
	for _, f := range fs {
		if f.Key == key && f.Level == l {
			return true
		}
	}
	return false
}

func TestWeakSecretsFail(t *testing.T) {
	for _, s := range []string{"", "devsecret", "short", "CHANGEME", strings.Repeat("a", 31), strings.Repeat("ab", 30), "change-me-" + strings.Repeat("x7Qm2vLp", 4)} {
		if !find(Audit(env(map[string]string{"JWT_SIGNING_SECRET": s})), "JWT_SIGNING_SECRET", Fail) {
			t.Errorf("JWT secret %q accepted", s)
		}
	}
	if find(Audit(env(map[string]string{"JWT_SIGNING_SECRET": "x7Qm2vLp9Zr4Tn8Wc1Bd6Hs3Jk5Fg0Ya"})), "JWT_SIGNING_SECRET", Fail) {
		t.Error("a 33-character random secret was refused")
	}
}

func TestSecretsKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789ABCDEF"))
	rep := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if find(Audit(env(map[string]string{"SECRETS_KEY": good})), "SECRETS_KEY", Fail) {
		t.Error("good key refused")
	}
	for name, k := range map[string]string{"short": base64.StdEncoding.EncodeToString([]byte("abc")), "repeated": rep, "notb64": "%%%"} {
		if !find(Audit(env(map[string]string{"SECRETS_KEY": k})), "SECRETS_KEY", Fail) {
			t.Errorf("%s key accepted", name)
		}
	}
	if !find(Audit(env(nil)), "SECRETS_KEY", Warn) {
		t.Error("unset key not flagged")
	}
}

func TestOtherFindings(t *testing.T) {
	fs := Audit(env(map[string]string{
		"DATABASE_URL": "postgres://u:p@db.plant.local:5432/x", "MQTT_TLS": "false", "APP_PUBLIC_URL": "http://iot.plant.local",
		"SELF_SIGNUP": "1", "LOCAL_LOGIN": "1", "TEST_MAIL": "1",
	}))
	for _, c := range []struct {
		k string
		l Level
	}{{"DATABASE_URL", Warn}, {"MQTT_TLS", Warn}, {"APP_PUBLIC_URL", Warn}, {"SELF_SIGNUP", Warn}, {"LOCAL_LOGIN", Warn}, {"TEST_MAIL", Fail}} {
		if !find(fs, c.k, c.l) {
			t.Errorf("missing %s %s", c.l, c.k)
		}
	}
	// a local dev setup does not trip the transport warnings
	loc := Audit(env(map[string]string{"DATABASE_URL": "postgresql://postgres:@/postgres?host=/tmp/pgdata", "APP_PUBLIC_URL": "http://localhost:5173"}))
	if find(loc, "DATABASE_URL", Warn) || find(loc, "APP_PUBLIC_URL", Warn) {
		t.Errorf("local setup flagged: %v", loc)
	}
}

func TestNoSecretInOutput(t *testing.T) {
	out := Format(Audit(env(map[string]string{"JWT_SIGNING_SECRET": "devsecret", "DATABASE_URL": "postgres://u:hunter2@db.example.com/x"})))
	if strings.Contains(out, "hunter2") || strings.Contains(out, "devsecret") {
		t.Fatalf("report leaks a secret: %s", out)
	}
}
