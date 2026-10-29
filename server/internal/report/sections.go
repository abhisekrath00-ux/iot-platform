package report

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

// Text sections: up to 10 plain-text notes positioned before or after data.
// Trailing notes precede summaries/emissions. No markup or free-form canvas.

const (
	maxSections     = 10
	maxSectionTitle = 80
	maxSectionBody  = 2000
)

type Section struct {
	Title string `json:"title"`
	// Position is empty/after_data (legacy) or before_data.
	Position string `json:"position,omitempty"`
	Body     string `json:"body"`
	// PageBreak starts this section at the top of a new page in the PDF, and sets a
	// printed-page break in the HTML. CSV, XLSX, DOCX and PPTX ignore it (their pages
	// are the reader's). Subreport summaries never carry it.
	PageBreak bool `json:"page_break,omitempty"`
}

// SubreportRef points at another saved report whose summary is embedded.
type SubreportRef struct {
	ReportID string `json:"report_id"`
}

const maxSubreports = 5

// allSections is the trailing notes followed by embedded summaries.
func (d Definition) allSections() []Section {
	return append(d.sectionsAt(false), d.SubSummaries...)
}

// sectionsAt preserves relative order within each placement; summaries remain trailing.
func (d Definition) sectionsAt(before bool) []Section {
	out := []Section{}
	for _, s := range d.Sections {
		if (s.Position == "before_data") == before {
			out = append(out, s)
		}
	}
	return out
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
		if s.Position != "" && s.Position != "after_data" && s.Position != "before_data" {
			return fmt.Errorf("section %d: position must be before_data or after_data", i+1)
		}
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

func writeSectionsHTML(b *strings.Builder, d Definition) { writeSectionListHTML(b, d.allSections()) }
func writeSectionListHTML(b *strings.Builder, sections []Section) {
	for _, s := range sections {
		h2 := `<h2>`
		if s.PageBreak {
			h2 = `<h2 style="page-break-before:always;break-before:page">`
		}
		b.WriteString(h2 + html.EscapeString(strings.TrimSpace(s.Title)) + `</h2>`)
		for _, p := range sectionParas(s.Body) {
			b.WriteString(`<p>` + html.EscapeString(p) + `</p>`)
		}
	}
}

func writeSectionsCSV(b *strings.Builder, d Definition) { writeSectionListCSV(b, d.allSections()) }
func writeSectionListCSV(b *strings.Builder, sections []Section) {
	q := func(s string) string { return `"` + strings.ReplaceAll(CSVSafe(s), `"`, `""`) + `"` }
	for _, s := range sections {
		b.WriteString("\n")
		for _, l := range append([]string{strings.TrimSpace(s.Title)}, sectionParas(s.Body)...) {
			b.WriteString(q(l) + "\n")
		}
	}
}
