package docsrag

import "testing"

// Natural-language questions, written the way an operator would ask, not copied keyword lists.
// This measures lexical source recall@1/@3/@5 on the embedded docs. It does not measure answer
// quality, a language model, or semantic (vector) retrieval, which is not built.
var nlCases = []struct{ q, doc string }{
	{"How do I install this on a machine with no internet?", "airgap.md"},
	{"What is the default login the first time I start it?", "first-run-credentials.md"},
	{"How often should I test restoring a backup?", "backup-restore.md"},
	{"Can people sign in with our company SAML provider?", "sso-saml.md"},
	{"How do I give Modbus registers names and units?", "register-maps.md"},
	{"How do I roll out new firmware to a group of gateways?", "fleet.md"},
	{"What happens to an assistant request if the server restarts?", "assistant-runs.md"},
	{"Which assistant actions need my confirmation?", "assistant-action-outcomes.md"},
	{"Which functions can I use in a highlight rule?", "report-expressions.md"},
	{"How do I choose which local AI model to download?", "model-chooser.md"},
	{"How do I run several nodes on Proxmox?", "proxmox-multi-node.md"},
	{"Can the platform run without a GPU?", "model-chooser.md"},
	{"How do I use the terminal app?", "terminal-app.md"},
	{"What are the recovery time and recovery point targets?", "backup-restore.md"},
	{"How are devices authorized to publish on the MQTT broker?", "broker-acl.md"},
	{"How do I keep a device's desired state when it is offline?", "device-shadow.md"},
	{"How do I commission a new site?", "commissioning.md"},
	{"What does the platform do when a device stops reporting?", "escalation.md"},
	{"How do I scale ingestion to more devices?", "scaling.md"},
	{"How do I upgrade to a new version safely?", "try-and-upgrade.md"},
	{"How do I find an asset quickly across sites?", "search-reliability.md"},
	{"Where do I set the server address devices connect to?", "server-addresses.md"},
	{"What service level objectives does the API have?", "slo.md"},
	{"How do edge rules run when the gateway loses the network?", "edge-rules.md"},
	{"Which report builder features are not built yet?", "report-builder-parity.md"},
	{"How is the time series data stored?", "timeseries-store.md"},
	{"How do I run the tests?", "testing.md"},
	{"What does the assistant remember between chats?", "assistant-memory.md"},
}

func TestRetrievalNaturalLanguageRecall(t *testing.T) {
	ix, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	var at1, at3, at5 int
	for _, c := range nlCases {
		rank := 0
		for i, h := range ix.Search(c.q, 5) {
			if h.Doc == c.doc {
				rank = i + 1
				break
			}
		}
		switch {
		case rank == 1:
			at1++
			fallthrough
		case rank >= 2 && rank <= 3:
			at3++
			fallthrough
		case rank >= 4:
			at5++
		}
		if rank == 0 {
			t.Logf("MISS top5: %q want %s", c.q, c.doc)
		} else if rank > 1 {
			t.Logf("rank %d: %q want %s", rank, c.q, c.doc)
		}
	}
	n := len(nlCases)
	t.Logf("recall@1 %d/%d  @3 %d/%d  @5 %d/%d", at1, n, at3, n, at5, n)
	if at5*100 < n*minRecall5 {
		t.Fatalf("recall@5 %d/%d below floor %d%%", at5, n, minRecall5)
	}
}

const minRecall5 = 75 // measured 22/28 (79%); lowering this needs a stated reason
