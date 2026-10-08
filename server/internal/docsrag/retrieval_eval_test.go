package docsrag

import "testing"

// Retrieval only: lexical source recall, not answer accuracy, model obedience or vector quality.
func TestRetrievalReliabilitySourceRecall(t *testing.T) {
	ix, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ q, doc string }{
		{"model chooser download checksum size", "model-chooser.md"},
		{"agent oversized batch cancellation JSON arguments", "agent-failure-matrix.md"},
		{"entity search redirects tenant response limit", "search-reliability.md"},
		{"backup restore database", "backup-restore.md"},
		{"Modbus register map CSV import", "register-maps.md"},
		{"air gapped bundle internet", "airgap.md"},
		{"SAML identity provider single sign on", "sso-saml.md"},
		{"highlight rule formula row avg max if and or not", "report-expressions.md"},
		{"clamp sqrt round expression function list", "report-expressions.md"},
		{"firmware staged rollout rollback", "fleet.md"},
	}
	for _, c := range cases {
		t.Run(c.doc, func(t *testing.T) {
			hits := ix.Search(c.q, 5)
			for _, h := range hits {
				if h.Doc == c.doc {
					if h.Heading == "" || h.Snippet == "" {
						t.Fatal("source lacks evidence")
					}
					return
				}
			}
			t.Fatalf("source absent in top5: %+v", hits)
		})
	}
	if hits := ix.Search("zyxwunicornalien42", 5); len(hits) != 0 {
		t.Fatalf("unknown query should have no evidence: %+v", hits)
	}
}

func TestRetrievalRefusesInvalidResultLimit(t *testing.T) {
	ix := New([]Passage{{Doc: "test", Heading: "Pump", Text: "Pump meter"}})
	for _, k := range []int{-1, 0} {
		if len(ix.Search("pump", k)) != 0 {
			t.Fatalf("invalid limit %d", k)
		}
	}
}
