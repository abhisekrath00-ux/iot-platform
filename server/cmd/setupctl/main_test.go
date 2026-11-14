package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInvalidInvocationAndEffectAcknowledgementRefuseBeforeDB(t *testing.T) {
	for _, args := range [][]string{nil, {"inspect-reviewed", "extra"}, {"apply-reviewed", "--yes"}, {"anything"}} {
		if e := run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for _, body := range []string{"", `{}`, `{"version":1,"scope":"transport_only","writers_stopped":true,"backup_verified":true}`, `{"version":1,"scope":"application_bootstrap","writers_stopped":true,"backup_verified":true,"approval":true}`, `{"version":1,"scope":"application_bootstrap","writers_stopped":true,"backup_verified":true}{}`} {
		e := run(context.Background(), []string{"apply-reviewed"}, strings.NewReader(body), &bytes.Buffer{})
		if e == nil || !strings.Contains(e.Error(), "acknowledgement") {
			t.Fatalf("untrusted acknowledgement accepted: %v", e)
		}
	}
}

func TestStagedProcessingAcknowledgementIsSeparateFromExisting93(t *testing.T) {
	for _, body := range []string{"", `{}`, `{"version":1,"scope":"application_bootstrap","writers_stopped":true,"backup_verified":true}`, `{"version":1,"scope":"staged_processing_v1","writers_stopped":true,"backup_verified":true,"approval":true}`, `{"version":1,"scope":"staged_processing_v1","writers_stopped":true,"backup_verified":true}{}`} {
		if err := run(context.Background(), []string{"apply-staged-processing"}, strings.NewReader(body), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
			t.Fatal("invalid staged acknowledgement accepted", err)
		}
	}
	t.Setenv("DATABASE_URL", "")
	for _, action := range []string{"inspect-staged-processing", "apply-staged-processing"} {
		if err := run(context.Background(), []string{action}, strings.NewReader(`{"version":1,"scope":"staged_processing_v1","writers_stopped":true,"backup_verified":true}`), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "explicit database") {
			t.Fatal(action, err)
		}
	}
}
