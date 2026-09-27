package enroll

import (
	"strings"
	"testing"
	"time"
)

func TestNewCodeFormatAndUniqueness(t *testing.T) {
	a, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewCode()
	if a == b {
		t.Fatal("codes collide")
	}
	if len(a) != 39 || strings.Count(a, "-") != 7 { // 32 chars, 7 dashes
		t.Fatalf("unexpected format %q", a)
	}
	for _, bad := range []string{"0", "1", "O", "I"} {
		if strings.Contains(strings.ReplaceAll(a, "-", ""), bad) {
			t.Fatalf("ambiguous char %q in %q", bad, a)
		}
	}
}

func TestHashMatchNormalizes(t *testing.T) {
	code, _ := NewCode()
	h := Hash(code)
	lower := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	if !MatchHash(lower, h) {
		t.Fatal("normalized code should match")
	}
	if MatchHash("AAAA-AAAA-AAAA-AAAA", h) {
		t.Fatal("wrong code matched")
	}
}

func TestCheckRedeemable(t *testing.T) {
	now := time.Now()
	if err := CheckRedeemable(now.Add(time.Hour), nil, now); err != nil {
		t.Fatal(err)
	}
	if err := CheckRedeemable(now.Add(-time.Minute), nil, now); err != ErrExpired {
		t.Fatalf("want expired, got %v", err)
	}
	claimed := now.Add(-time.Minute)
	if err := CheckRedeemable(now.Add(time.Hour), &claimed, now); err != ErrAlreadyUsed {
		t.Fatalf("want already-used, got %v", err)
	}
}
