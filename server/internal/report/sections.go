package report

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

// Text sections: up to 10 titled notes (commentary, method, caveats) printed after the data and before the
// emissions section in every format. Plain text only: no markup, no fields, no links; every format escapes it.
// This is not a free-form canvas or an ordering of the built-in blocks.

const (
	maxSections     = 10
	maxSectionTitle = 80
	maxSectionBody  = 2000
)

type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// SubreportRef points at another saved report whose summary is embedded.
type SubreportRef struct {
	ReportID string `json:"report_id"`
}

const maxSubreports = 5

// allSections is the typed notes followed by the embedded subreport summaries, as the renderers print them.
func (d Definition) allSections() []Section {
	return append(append([]Section{}, d.Sections...), d.SubSummaries...)
}

func validateSubreports(d Definition) error {
	if len(d.Subreports) > maxSubreports {
		return fmt.Errorf("at most %d subreports", maxSubreports)
	}
	seen := map[string]bool{}
	for i, r := range d.Subreports {
		if !validID(r.ReportID) {
			return fmt.Errorf("subreport %d: a report id is required", i+1)
		}
		if seen[r.ReportID] {
			return fmt.Errorf("subreport %d: the same report is listed twice", i+1)
		}
		seen[r.ReportID] = true
	}
	return nil
}

func validateSections(d Definition) error {
	if err := validateSubreports(d); err != nil {
		return err
	}
	if len(d.Sections) > maxSections {
		return fmt.Errorf("at most %d text sections", maxSections)
	}
	for i, s := range d.Sections {
		t, b := strings.TrimSpace(s.Title), strings.TrimSpace(s.Body)
		if t == "" || utf8.RuneCountInString(t) > maxSectionTitle || strings.ContainsAny(t, "\r\n") {
			return fmt.Errorf("section %d: a one-line title of up to %d characters is required", i+1, maxSectionTitle)
		}
		if b == "" || utf8.RuneCountInString(b) > maxSectionBody {
			return fmt.Errorf("section %d: text of up to %d characters is required", i+1, maxSectionBody)
		}
		for _, r := range b {
			if r < 32 && r != '\n' && r != '\t' {
				return fmt.Errorf("section %d: text has a control character", i+1)
			}
		}
	}
	return nil
}

// sectionParas splits a body into non-empty lines.
func sectionParas(body string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func writeSectionsHTML(b *strings.Builder, d Definition) {
	for _, s := range d.allSections() {
		b.WriteString(`<h2>` + html.EscapeString(strings.TrimSpace(s.Title)) + `</h2>`)
		for _, p := range sectionParas(s.Body) {
			b.WriteString(`<p>` + html.EscapeString(p) + `</p>`)
		}
	}
}

func writeSectionsCSV(b *strings.Builder, d Definition) {
	q := func(s string) string { return `"` + strings.ReplaceAll(CSVSafe(s), `"`, `""`) + `"` }
	for _, s := range d.allSections() {
		b.WriteString("\n")
		for _, l := range append([]string{strings.TrimSpace(s.Title)}, sectionParas(s.Body)...) {
			b.WriteString(q(l) + "\n")
		}
	}
}
