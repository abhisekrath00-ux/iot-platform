package totp

import (
	"strings"
	"testing"
	"time"
)

func TestRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890") // the RFC's SHA-1 test key; 6-digit values are the last 6 of its 8-digit table
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		if got := CodeAt(secret, unix/30); got != want {
			t.Errorf("t=%d: got %s want %s", unix, got, want)
		}
	}
}

func TestVerifyWindowAndReplay(t *testing.T) {
	secret := NewSecret()
	now := time.Unix(1700000000, 0)
	cur := Step(now)
	step, ok := Verify(secret, CodeAt(secret, cur), now, 0)
	if !ok || step != cur {
		t.Fatal("current code refused")
	}
	if _, ok := Verify(secret, CodeAt(secret, cur), now, cur); ok {
		t.Fatal("the same step must not verify twice")
	}
	if _, ok := Verify(secret, CodeAt(secret, cur-1), now, 0); !ok {
		t.Fatal("one step of drift should pass")
	}
	if _, ok := Verify(secret, CodeAt(secret, cur-3), now, 0); ok {
		t.Fatal("an old code passed")
	}
	if _, ok := Verify(secret, "12345", now, 0); ok {
		t.Fatal("short code passed")
	}
	if _, ok := Verify(secret, " "+CodeAt(secret, cur)[:3]+" "+CodeAt(secret, cur)[3:], now, 0); !ok {
		t.Fatal("a code typed with a space should pass")
	}
}

func TestURI(t *testing.T) {
	u := URI("Hexmon IoT", "a@b.c", []byte("12345678901234567890"))
	if !strings.HasPrefix(u, "otpauth://totp/Hexmon%20IoT:a@b.c?") || !strings.Contains(u, "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ") {
		t.Fatal(u)
	}
}
