package diagnose

import (
	"encoding/csv"
	"io"
	"strings"
)

// WriteCSV writes the findings one row each. Cells that start with = + - @ are prefixed with a
// single quote so a spreadsheet never runs device-supplied text as a formula.
func (r Report) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	safe := func(s string) string {
		if s != "" && strings.ContainsAny(s[:1], "=+-@\t\r") {
			return "'" + s
		}
		return s
	}
	cw.Write([]string{"scope", "severity", "problem", "evidence", "likely_cause", "recommended_action", "fix", "fix_policy"})
	for _, f := range r.Findings {
		cw.Write([]string{safe(r.Scope), f.Severity, safe(f.Problem), safe(strings.Join(f.Evidence, " | ")), safe(f.LikelyCause), safe(f.Recommended), safe(f.PossibleFix.Action), f.PossibleFix.Policy})
	}
	cw.Flush()
	return cw.Error()
}
