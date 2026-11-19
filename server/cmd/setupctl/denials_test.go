package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDenialMaintenanceRefusesBeforeDB(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, body := range []string{"", `{}`, `{"version":1,"scope":"staged_processing_v1","writers_stopped":true,"backup_verified":true}`, `{"version":1,"scope":"staged_denial_v1","writers_stopped":true,"backup_verified":true,"approval":true}`, `{"version":1,"scope":"staged_denial_v1","writers_stopped":true,"backup_verified":true}{}`} {
		if e := run(context.Background(), []string{"apply-staged-denials"}, strings.NewReader(body), &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "acknowledgement") {
			t.Fatal(e)
		}
	}
	for _, action := range []string{"inspect-staged-denials", "apply-staged-denials", "verify-staged-denials"} {
		body := `{"version":1,"scope":"staged_denial_v1","writers_stopped":true,"backup_verified":true}`
		if action == "verify-staged-denials" {
			body = `{}`
		}
		if e := run(context.Background(), []string{action}, strings.NewReader(body), &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "explicit database") {
			t.Fatal(action, e)
		}
	}
	for _, body := range []string{"", `{} {}`, `{"arbitrary":true}`, strings.Repeat("x", 4098)} {
		if e := run(context.Background(), []string{"verify-staged-denials"}, strings.NewReader(body), &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "evidence") {
			t.Fatal(e)
		}
	}
}
