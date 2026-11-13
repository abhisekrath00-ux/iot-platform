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
