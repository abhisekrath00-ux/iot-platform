package report

import (
	"fmt"
	"html"
	"math"
	"strings"
)

// Emissions section (GHG Protocol Scope 1 and 2 style). It turns metered quantities (electricity in kWh,
// fuel in litres, and so on) into tonnes of CO2e using emission factors THE OPERATOR ENTERS, each with a
// free-text source. The platform ships no factor values: grid and fuel factors change every year and
// differ by country, so any number built in would be a guess. This is a calculation aid. It is not a
// certified or assured BRSR or GHG report and it does not produce the BRSR filing format.

const EmissionsNotice = "Calculated from metered readings and the emission factors entered by the operator. The platform supplies no factors. A calculation aid, not a certified or assured BRSR or GHG report."

const maxEmissionSources = 20

// EmissionSource is one metered source.
type EmissionSource struct {
	Name         string  `json:"name"`
	Scope        int     `json:"scope"`          // 1 (direct fuel, company vehicles, process) or 2 (purchased electricity)
	Metric       Metric  `json:"metric"`         // must also be one of the report's metrics
	Mode         string  `json:"mode,omitempty"` // "sum" (readings are amounts per interval, default) or "delta" (cumulative counter: last max minus first min)
	Unit         string  `json:"unit"`           // unit of the quantity after Scale, e.g. kWh, L, kg, m3
	Scale        float64 `json:"scale,omitempty"`
	Factor       float64 `json:"factor"`        // kg CO2e per Unit
	FactorSource string  `json:"factor_source"` // where the operator got the factor, printed in the report
}

// EmissionsIntensity divides total tCO2e by a denominator quantity such as production units or turnover.
type EmissionsIntensity struct {
	Label  string  `json:"label"` // e.g. "per tonne produced"
	Metric Metric  `json:"metric"`
	Mode   string  `json:"mode,omitempty"`
	Scale  float64 `json:"scale,omitempty"`
}

type EmissionRow struct {
	Scope    int
	Name     string
	Point    string
	Quantity float64
	Unit     string
	Factor   float64
	Tonnes   float64
	Note     string
}

type EmissionsResult struct {
	Rows      []EmissionRow
	Scope1    float64
	Scope2    float64
	Total     float64
	Intensity string // "" when not configured or not computable
	Sources   []string
}

func finitePos(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && f > 0 }

func validText(s string, max int) bool {
	if strings.TrimSpace(s) == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func validateEmissions(d Definition) error {
	if len(d.Emissions) == 0 {
		if d.EmissionsIntensity != nil {
			return fmt.Errorf("emissions intensity needs at least one emission source")
		}
		return nil
	}
	if len(d.Emissions) > maxEmissionSources {
		return fmt.Errorf("at most %d emission sources", maxEmissionSources)
	}
	have := map[Metric]bool{}
	for _, m := range d.Metrics {
		have[m] = true
	}
	for i, s := range d.Emissions {
		n := i + 1
		if !validText(s.Name, 64) {
			return fmt.Errorf("emission source %d: a name of 1-64 printable characters is required", n)
		}
		if s.Scope != 1 && s.Scope != 2 {
			return fmt.Errorf("emission source %q: scope must be 1 or 2", s.Name)
		}
		if !have[s.Metric] {
			return fmt.Errorf("emission source %q: its metric must also be a metric of this report", s.Name)
		}
		if s.Mode != "" && s.Mode != "sum" && s.Mode != "delta" {
			return fmt.Errorf("emission source %q: mode must be sum or delta", s.Name)
		}
		if !validText(s.Unit, 16) {
			return fmt.Errorf("emission source %q: a unit (at most 16 characters) is required", s.Name)
		}
		if s.Scale != 0 && !finitePos(s.Scale) {
			return fmt.Errorf("emission source %q: scale must be a positive number", s.Name)
		}
		if !finitePos(s.Factor) || s.Factor > 1e6 {
			return fmt.Errorf("emission source %q: the emission factor (kg CO2e per unit) must be a positive number you enter", s.Name)
		}
		if !validText(s.FactorSource, 120) {
			return fmt.Errorf("emission source %q: say where the factor comes from (at most 120 characters)", s.Name)
		}
	}
	if in := d.EmissionsIntensity; in != nil {
		if !validText(in.Label, 40) {
			return fmt.Errorf("emissions intensity needs a label of at most 40 characters")
		}
		if !have[in.Metric] {
			return fmt.Errorf("emissions intensity: its metric must also be a metric of this report")
		}
		if in.Mode != "" && in.Mode != "sum" && in.Mode != "delta" {
			return fmt.Errorf("emissions intensity: mode must be sum or delta")
		}
		if in.Scale != 0 && !finitePos(in.Scale) {
			return fmt.Errorf("emissions intensity: scale must be a positive number")
		}
	}
	return nil
}

// quantity totals a metric over the window. ok is false when there is no data or a counter went backwards.
func quantity(bs []Bucket, mode string) (q float64, note string, ok bool) {
	var first, last *Bucket
	for i := range bs {
		if bs[i].Count == 0 {
			continue
		}
		if first == nil {
			first = &bs[i]
		}
		last = &bs[i]
		if mode != "delta" {
			q += bs[i].Sum
		}
	}
	if first == nil {
		return 0, "no data", false
	}
	if mode == "delta" {
		q = last.Max - first.Min
		if q < 0 {
			return 0, "counter went backwards (reset?)", false
		}
	}
	return q, "", true
}

func scaleOr1(s float64) float64 {
	if s == 0 {
		return 1
	}
	return s
}

// BuildEmissions computes every source. A source with no data or a reset counter is listed with a
// note and counts as zero; the note says so, so a gap is never silent.
func BuildEmissions(d Definition, series map[Metric][]Bucket) EmissionsResult {
	var res EmissionsResult
	for _, s := range d.Emissions {
		q, note, ok := quantity(series[s.Metric], s.Mode)
		q *= scaleOr1(s.Scale)
		t := 0.0
		if ok {
			t = q * s.Factor / 1000
		}
		res.Rows = append(res.Rows, EmissionRow{Scope: s.Scope, Name: s.Name, Point: s.Metric.DeviceID + " / " + s.Metric.PointID,
			Quantity: q, Unit: s.Unit, Factor: s.Factor, Tonnes: t, Note: note})
		if s.Scope == 1 {
			res.Scope1 += t
		} else {
			res.Scope2 += t
		}
		res.Sources = append(res.Sources, fmt.Sprintf("%s: %.6g kg CO2e per %s, source: %s", s.Name, s.Factor, s.Unit, s.FactorSource))
	}
	res.Total = res.Scope1 + res.Scope2
	if in := d.EmissionsIntensity; in != nil {
		if q, _, ok := quantity(series[in.Metric], in.Mode); ok && q*scaleOr1(in.Scale) > 0 {
			res.Intensity = fmt.Sprintf("%s: %.6g tCO2e %s (Scope 1 + 2 divided by %.6g)", "Intensity", res.Total/(q*scaleOr1(in.Scale)), in.Label, q*scaleOr1(in.Scale))
		} else {
			res.Intensity = "Intensity " + in.Label + ": not computed (no data for the denominator)"
		}
	}
	return res
}

func EmissionsHeader() []string {
	return []string{"scope", "source", "point", "quantity", "unit", "factor (kg CO2e/unit)", "tCO2e", "note"}
}

// EmissionsCells formats the rows plus the Scope 1, Scope 2 and total lines.
func EmissionsCells(r EmissionsResult) [][]string {
	out := [][]string{}
	for _, x := range r.Rows {
		out = append(out, []string{fmt.Sprint(x.Scope), x.Name, x.Point, fmt.Sprintf("%.3f", x.Quantity), x.Unit, fmt.Sprintf("%.6g", x.Factor), fmt.Sprintf("%.4f", x.Tonnes), x.Note})
	}
	t := func(l string, v float64) []string { return []string{"", l, "", "", "", "", fmt.Sprintf("%.4f", v), ""} }
	return append(out, t("Scope 1 total", r.Scope1), t("Scope 2 total", r.Scope2), t("Scope 1 + 2 total", r.Total))
}

func writeEmissionsHTML(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	if len(d.Emissions) == 0 {
		return
	}
	r := BuildEmissions(d, series)
	b.WriteString(`<h2>Emissions (Scope 1 and 2)</h2><table><tr>`)
	for _, h := range EmissionsHeader() {
		b.WriteString(`<th>` + html.EscapeString(h) + `</th>`)
	}
	b.WriteString(`</tr>`)
	cells := EmissionsCells(r)
	for i, c := range cells {
		style := ""
		if i >= len(r.Rows) {
			style = ` style="font-weight:600;background:#fafafa"`
		}
		b.WriteString(`<tr` + style + `>`)
		for _, v := range c {
			b.WriteString(`<td>` + html.EscapeString(v) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)
	if r.Intensity != "" {
		b.WriteString(`<p>` + html.EscapeString(r.Intensity) + `</p>`)
	}
	b.WriteString(`<p class="meta">Factors used:</p><ul class="meta">`)
	for _, s := range r.Sources {
		b.WriteString(`<li>` + html.EscapeString(s) + `</li>`)
	}
	b.WriteString(`</ul><p class="meta">` + html.EscapeString(EmissionsNotice) + `</p>`)
}

func writeEmissionsCSV(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	if len(d.Emissions) == 0 {
		return
	}
	q := func(s string) string { return `"` + strings.ReplaceAll(CSVSafe(s), `"`, `""`) + `"` }
	r := BuildEmissions(d, series)
	b.WriteString("\n")
	line := func(cs []string) {
		o := make([]string, len(cs))
		for i, c := range cs {
			o[i] = q(c)
		}
		b.WriteString(strings.Join(o, ",") + "\n")
	}
	line(EmissionsHeader())
	for _, c := range EmissionsCells(r) {
		line(c)
	}
	if r.Intensity != "" {
		line([]string{r.Intensity})
	}
	for _, s := range r.Sources {
		line([]string{"factor", s})
	}
	line([]string{EmissionsNotice})
}
