package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSelectedIdentityMaintenanceRefusesBeforeDB(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, body := range []string{"", `{} {}`, `{"unknown":true}`, strings.Repeat("x", 4098)} {
		if err := run(t.Context(), []string{"verify-staged-identity"}, strings.NewReader(body), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "evidence") {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"inspect-staged-identity", "verify-staged-identity"} {
		body := ""
		if action == "verify-staged-identity" {
			body = `{}`
		}
		if err := run(t.Context(), []string{action}, strings.NewReader(body), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "explicit database") {
			t.Fatal(action, err)
		}
	}
	for _, body := range []string{`{}`, `{"activate":true}`, strings.Repeat("x", 4098)} {
		if err := run(context.Background(), []string{"inspect-staged-identity"}, strings.NewReader(body), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no input") {
			t.Fatal(err)
		}
	}
	if err := run(t.Context(), []string{"apply-staged-identity"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("identity apply action exists")
	}
}
