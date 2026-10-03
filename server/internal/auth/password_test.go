package auth

import "testing"

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery", h) || VerifyPassword("correct horse batterz", h) || VerifyPassword("", h) {
		t.Fatal("verify")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("same salt twice")
	}
	for _, bad := range []string{"", "x", "pbkdf2-sha256$1$AA$AA", "md5$600000$AAAAAAAAAAAAAAAAAAAAAA$AA", "pbkdf2-sha256$600000$!!$AA"} {
		if VerifyPassword("correct horse battery", bad) {
			t.Errorf("malformed hash %q verified", bad)
		}
	}
	// the stored iteration count is honoured
	low, _ := hashWith("pw-long-enough-1", []byte("0123456789abcdef"), 150000)
	if !VerifyPassword("pw-long-enough-1", low) {
		t.Fatal("custom iteration hash")
	}
	if CheckPasswordPolicy("short", "a@b.c") == nil || CheckPasswordPolicy("A@B.C", "a@b.c") == nil || CheckPasswordPolicy("a-long-enough-password", "a@b.c") != nil {
		t.Fatal("policy")
	}
}
