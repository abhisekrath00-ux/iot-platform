package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCommandTopicIsTenantAndGatewayScoped(t *testing.T) {
	if got := commandTopic("t1", "gw-9"); got != "t/t1/g/gw-9/cmd" {
		t.Fatalf("topic %q", got)
	}
}

func TestCommandEnvelopeHasAllRequiredFields(t *testing.T) {
	now := time.Now().UTC()
	b, _ := json.Marshal(commandEnvelope{RequestID: "r", Target: "d", Action: "open", Parameters: json.RawMessage(`{"a":1}`),
		ApprovedBy: "u2", IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), PolicyVersion: "v1"})
	for _, f := range []string{"request_id", "target", "action", "parameters", "approved_by", "issued_at", "expires_at", "policy_version"} {
		if !strings.Contains(string(b), `"`+f+`"`) {
			t.Fatalf("missing %s in %s", f, b)
		}
	}
}
