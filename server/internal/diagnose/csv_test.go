package diagnose

import (
	"strings"
	"testing"
)

func TestCSVNeutralisesFormulas(t *testing.T) {
	r := Report{Scope: "=HYPERLINK(1)", Findings: []Finding{{Problem: "p, with comma", Evidence: []string{"+cmd|' /C calc'!A0", "b"}, Severity: "info", PossibleFix: Fix{Action: "@x", Policy: "NEVER_AUTO"}}}}
	var b strings.Builder
	if err := r.WriteCSV(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "'=HYPERLINK(1)") || !strings.Contains(out, "'+cmd") || !strings.Contains(out, "'@x") || !strings.Contains(out, `"p, with comma"`) {
		t.Fatalf("%s", out)
	}
}
