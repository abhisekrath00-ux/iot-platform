package report

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/xml"
	"fmt"
	"image"
	"image/png"
	"math"
	"strings"
	"time"
)

// RenderXLSX writes a minimal, valid .xlsx workbook using only the standard
// library (no new dependency, works air-gapped). One sheet, header row, one row
// per bucket. Text goes in as inline strings, which Excel never evaluates as
// formulas, so there is no formula-injection path.
func RenderXLSX(d Definition, series map[Metric][]Bucket) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}
	const hdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	var logoPNG []byte
	if d.Logo != nil {
		var pb bytes.Buffer
		if png.Encode(&pb, d.Logo) == nil && pb.Len() < 400<<10 {
			logoPNG = pb.Bytes()
		}
	}
	files := []struct{ n, b string }{
		{"[Content_Types].xml", hdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", hdr + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Report" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
	}
	var charts []xlChart
	// flush writes every package part once the sheet is known: the logo and the native charts
	// share one drawing part.
	flush := func() error {
		hasDraw := logoPNG != nil || len(charts) > 0
		var dRels, anchors strings.Builder
		ctStr := files[0].b
		ct := &ctStr
		if hasDraw {
			*ct = strings.Replace(*ct, `</Types>`, `<Override PartName="/xl/drawings/drawing1.xml" ContentType="application/vnd.openxmlformats-officedocument.drawing+xml"/></Types>`, 1)
			files = append(files, struct{ n, b string }{"xl/worksheets/_rels/sheet1.xml.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing" Target="../drawings/drawing1.xml"/></Relationships>`})
		}
		startRow := 0
		if logoPNG != nil {
			*ct = strings.Replace(*ct, `<Default Extension="xml"`, `<Default Extension="png" ContentType="image/png"/><Default Extension="xml"`, 1)
			w, h := logoSize(d.Logo)
			dRels.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/logo.png"/>`)
			fmt.Fprintf(&anchors, `<xdr:oneCellAnchor><xdr:from><xdr:col>10</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>0</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from><xdr:ext cx="%d" cy="%d"/><xdr:pic><xdr:nvPicPr><xdr:cNvPr id="2" name="Logo"/><xdr:cNvPicPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:embed="rId1"/><a:stretch><a:fillRect/></a:stretch></xdr:blipFill><xdr:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></xdr:spPr></xdr:pic><xdr:clientData/></xdr:oneCellAnchor>`, int(w*12700), int(h*12700), int(w*12700), int(h*12700))
			startRow = int(h/20) + 2
		}
		for i, c := range charts {
			rid := fmt.Sprintf("rId%d", i+2)
			ct2 := fmt.Sprintf(`<Override PartName="/xl/charts/chart%d.xml" ContentType="application/vnd.openxmlformats-officedocument.drawingml.chart+xml"/>`, i+1)
			*ct = strings.Replace(*ct, `</Types>`, ct2+`</Types>`, 1)
			fmt.Fprintf(&dRels, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="../charts/chart%d.xml"/>`, rid, i+1)
			r0 := startRow + i*18
			fmt.Fprintf(&anchors, `<xdr:twoCellAnchor><xdr:from><xdr:col>10</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from><xdr:to><xdr:col>18</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:to><xdr:graphicFrame macro=""><xdr:nvGraphicFramePr><xdr:cNvPr id="%d" name="Chart %d"/><xdr:cNvGraphicFramePr/></xdr:nvGraphicFramePr><xdr:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/></xdr:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart"><c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="%s"/></a:graphicData></a:graphic></xdr:graphicFrame><xdr:clientData/></xdr:twoCellAnchor>`, r0, r0+16, i+10, i+1, rid)
			files = append(files, struct{ n, b string }{fmt.Sprintf("xl/charts/chart%d.xml", i+1), hdr + xlChartXML(d.Chart, c)})
		}
		if hasDraw {
			files = append(files,
				struct{ n, b string }{"xl/drawings/_rels/drawing1.xml.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + dRels.String() + `</Relationships>`},
				struct{ n, b string }{"xl/drawings/drawing1.xml", hdr + `<xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` + anchors.String() + `</xdr:wsDr>`})
		}
		files[0].b = ctStr
		for _, f := range files {
			if err := add(f.n, f.b); err != nil {
				return err
			}
		}
		if logoPNG != nil {
			return add("xl/media/logo.png", string(logoPNG))
		}
		return nil
	}
	drawingTag := func() string {
		if logoPNG != nil || len(charts) > 0 {
			return `<drawing r:id="rId1"/>`
		}
		return ""
	}
	var sb strings.Builder
	sb.WriteString(hdr + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheetData>`)
	row := 0
	str := func(s string) string {
		var e bytes.Buffer
		xml.EscapeText(&e, []byte(s))
		return `<c t="inlineStr"><is><t>` + e.String() + `</t></is></c>`
	}
	num := func(f float64) string { return fmt.Sprintf(`<c><v>%.10g</v></c>`, f) }
	sectionRows := func() {
		for _, sc := range d.allSections() {
			row += 2
			sb.WriteString(fmt.Sprintf(`<row r="%d">`, row) + str(strings.TrimSpace(sc.Title)) + `</row>`)
			for _, p := range sectionParas(sc.Body) {
				row++
				sb.WriteString(fmt.Sprintf(`<row r="%d">`, row) + str(p) + `</row>`)
			}
		}
	}
	emissionsRows := func() {
		if len(d.Emissions) == 0 {
			return
		}
		er := BuildEmissions(d, series)
		row += 2
		sb.WriteString(fmt.Sprintf(`<row r="%d">`, row) + str("Emissions (Scope 1 and 2)") + `</row>`)
		row++
		fmt.Fprintf(&sb, `<row r="%d">`, row)
		for _, h := range EmissionsHeader() {
			sb.WriteString(str(h))
		}
		sb.WriteString(`</row>`)
		for _, c := range EmissionsCells(er) {
			row++
			fmt.Fprintf(&sb, `<row r="%d">`, row)
			for j, v := range c {
				if j == 3 || j == 5 || j == 6 {
					var f float64
					if _, err := fmt.Sscanf(v, "%g", &f); err == nil && v != "" {
						sb.WriteString(num(f))
						continue
					}
				}
				sb.WriteString(str(v))
			}
			sb.WriteString(`</row>`)
		}
		extra := append([]string{}, er.Sources...)
		if er.Intensity != "" {
			extra = append(extra, er.Intensity)
		}
		extra = append(extra, EmissionsNotice)
		for _, l := range extra {
			row++
			sb.WriteString(fmt.Sprintf(`<row r="%d">`, row) + str(l) + `</row>`)
		}
	}
	rollupRows := func() {
		if d.Rollup == "" {
			return
		}
		row += 2 // blank row, then the section
		fmt.Fprintf(&sb, `<row r="%d">`, row)
		for _, h := range RollupHeader(d) {
			sb.WriteString(str(h))
		}
		sb.WriteString(`</row>`)
		for _, r := range BuildRollup(d, series) {
			row++
			fmt.Fprintf(&sb, `<row r="%d">`, row)
			sb.WriteString(str(r.Group) + str(r.Point))
			sb.WriteString(num(float64(r.Devices)) + num(float64(r.Samples)) + num(r.Avg) + num(r.Min) + num(r.Max) + num(r.Sum))
			sb.WriteString(`</row>`)
		}
	}
	if d.Layout == "matrix" {
		hdr, rows, total := Matrix(d, series)
		emit := func(cells []string, numeric bool) {
			row++
			fmt.Fprintf(&sb, `<row r="%d">`, row)
			for i, c := range cells {
				var f float64
				if _, err := fmt.Sscanf(c, "%g", &f); numeric && i > 0 && err == nil && c != "-" {
					sb.WriteString(num(f))
				} else {
					sb.WriteString(str(c))
				}
			}
			sb.WriteString(`</row>`)
		}
		emit(append([]string{d.GroupBy}, hdr...), false)
		for _, r := range rows {
			emit(r, true)
		}
		emit(total, true)
		sectionRows()
		emissionsRows()
		rollupRows()
		sb.WriteString(`</sheetData>` + drawingTag() + `</worksheet>`)
		if err := add("xl/worksheets/sheet1.xml", sb.String()); err != nil {
			return nil, err
		}
		if err := flush(); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	row++
	fmt.Fprintf(&sb, `<row r="%d">`, row)
	for _, h := range []string{"device_id", "point_id", "bucket_start", "avg", "min", "max", "count", "sum"} {
		sb.WriteString(str(h))
	}
	sb.WriteString(`</row>`)
	for _, m := range d.Metrics {
		if xlChartKind(d.Chart) && len(series[m]) >= 2 && len(charts) < maxXLSXCharts {
			charts = append(charts, xlChart{Title: m.DeviceID + " / " + m.PointID, First: row + 1, Last: row + len(series[m])})
		}
		for _, k := range series[m] {
			row++
			fmt.Fprintf(&sb, `<row r="%d">`, row)
			sb.WriteString(str(m.DeviceID) + str(m.PointID) + str(k.Start.UTC().Format(time.RFC3339)))
			sb.WriteString(num(k.Avg) + num(k.Min) + num(k.Max) + num(float64(k.Count)) + num(k.Sum))
			sb.WriteString(`</row>`)
		}
	}
	sectionRows()
	emissionsRows()
	rollupRows()
	sb.WriteString(`</sheetData>` + drawingTag() + `</worksheet>`)
	if err := add("xl/worksheets/sheet1.xml", sb.String()); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderPDF writes a paginated PDF with a built-in font (no fonts or
// libraries to ship, works air-gapped): title block, one table per metric,
// a repeating column header, and "Page n of m" on every page. Text is limited
// to printable ASCII; other characters become '?'.
func RenderPDF(title string, d Definition, series map[Metric][]Bucket, generated time.Time) []byte {
	return RenderPDFLogo(title, d, series, generated, d.Logo)
}

// RenderPDFLogo is RenderPDF with an optional logo (decoded PNG or JPEG) drawn top right on
// the first page. The logo is embedded as a Flate-compressed RGB image, composited over white.
func RenderPDFLogo(title string, d Definition, series map[Metric][]Bucket, generated time.Time, logo image.Image) []byte {
	pageW, pageH := PageSize(d.Page)
	perPage := 52 + (pageH-842)/14 // 52 lines on the default A4 portrait page
	if perPage < 20 {
		perPage = 20
	}
	dark := d.Theme == "dark"
	type line struct {
		text  string
		bold  bool
		chart []Bucket // non-nil on the first of chartSlots slots reserved for a line chart
		title string
	}
	var pages [][]line
	cur := []line{}
	flush := func() {
		if len(cur) > 0 {
			pages = append(pages, cur)
		}
		cur = nil
	}
	put := func(s string, bold bool) {
		if len(cur) >= perPage {
			flush()
		}
		cur = append(cur, line{text: s, bold: bold})
	}
	// putChart reserves chartSlots lines (a chart never splits across pages).
	putChart := func(title string, rows []Bucket) {
		if len(rows) == 0 {
			return
		}
		if len(cur)+chartSlots > perPage {
			flush()
		}
		cur = append(cur, line{chart: rows, title: title})
		for i := 1; i < chartSlots; i++ {
			cur = append(cur, line{})
		}
	}
	put(title, true)
	put(fmt.Sprintf("Generated %s  window %dh  grouped by %s", generated.UTC().Format(time.RFC3339), d.WindowHours, d.GroupBy), false)
	put("", false)
	if d.Insights {
		put("Insights", true)
		put("Statistical summary of bucketed averages in this window.", false)
		put("It describes what the data did, not why, and it predicts nothing.", false)
		if lines := InsightLines(Insights(d, series)); len(lines) == 0 {
			put("no data in window", false)
		} else {
			for _, l := range lines {
				put(l, false)
			}
		}
		put("", false)
	}
	if d.Layout == "matrix" {
		for i, m := range d.Metrics {
			if i >= 4 {
				put(fmt.Sprintf("(charts shown for the first 4 of %d metrics)", len(d.Metrics)), false)
				break
			}
			putChart(m.DeviceID+" / "+m.PointID, series[m])
		}
		hdr, rows, total := Matrix(d, series)
		const chunk = 5
		for c0 := 0; c0 < len(hdr) || c0 == 0; c0 += chunk {
			c1 := c0 + chunk
			if c1 > len(hdr) {
				c1 = len(hdr)
			}
			fmtRow := func(r []string) string {
				var sb strings.Builder
				fmt.Fprintf(&sb, "%-17s", r[0])
				for _, c := range r[1:][c0:c1] {
					fmt.Fprintf(&sb, " %16s", c)
				}
				return sb.String()
			}
			head := []string{d.GroupBy}
			for _, h := range hdr {
				if len(h) > 16 {
					h = h[:16]
				}
				head = append(head, h)
			}
			put(fmt.Sprintf("columns %d-%d of %d", c0+1, c1, len(hdr)), true)
			put(fmtRow(head), true)
			for _, r := range rows {
				if len(cur) >= perPage {
					flush()
					put(fmtRow(head), true)
				}
				put(fmtRow(r), false)
			}
			put(fmtRow(total), true)
			put("", false)
			if len(hdr) == 0 {
				break
			}
		}
	}
	colHdr := fmt.Sprintf("%-17s %10s %10s %10s %12s %8s", "bucket (UTC)", "avg", "min", "max", "sum", "samples")
	for _, m := range d.Metrics {
		if d.Layout == "matrix" {
			break
		}
		rows := series[m]
		put(m.DeviceID+" / "+m.PointID, true)
		if len(rows) == 0 {
			put("no data in window", false)
			put("", false)
			continue
		}
		putChart(m.DeviceID+" / "+m.PointID, rows)
		put(colHdr, true)
		for _, r := range rows {
			if len(cur) >= perPage {
				flush()
				put(m.DeviceID+" / "+m.PointID+" (cont.)", true)
				put(colHdr, true)
			}
			put(fmt.Sprintf("%-17s %10.3f %10.3f %10.3f %12.3f %8d", r.Start.UTC().Format("2006-01-02 15:04"), r.Avg, r.Min, r.Max, r.Sum, r.Count), false)
		}
		a, mn, mx, sm, n := Summary(rows)
		put(fmt.Sprintf("%-17s %10.3f %10.3f %10.3f %12.3f %8d", "overall", a, mn, mx, sm, n), true)
		put("", false)
	}
	for _, sc := range d.allSections() {
		put(strings.TrimSpace(sc.Title), true)
		for _, p := range sectionParas(sc.Body) {
			for len(p) > 88 {
				cut := strings.LastIndex(p[:88], " ")
				if cut < 20 {
					cut = 88
				}
				put(p[:cut], false)
				p = strings.TrimLeft(p[cut:], " ")
			}
			put(p, false)
		}
		put("", false)
	}
	if len(d.Emissions) > 0 {
		er := BuildEmissions(d, series)
		put("Emissions (Scope 1 and 2)", true)
		eh := fmt.Sprintf("%-5s %-22s %12s %-6s %10s %10s", "scope", "source", "quantity", "unit", "kg/unit", "tCO2e")
		put(eh, true)
		for i, x := range er.Rows {
			n := x.Name
			if len(n) > 22 {
				n = n[:22]
			}
			u := x.Unit
			if len(u) > 6 {
				u = u[:6]
			}
			l := fmt.Sprintf("%-5d %-22s %12.3f %-6s %10.4g %10.4f", x.Scope, n, x.Quantity, u, x.Factor, x.Tonnes)
			if x.Note != "" {
				l += "  (" + x.Note + ")"
			}
			put(l, false)
			_ = i
		}
		put(fmt.Sprintf("Scope 1 total %.4f tCO2e   Scope 2 total %.4f tCO2e   Scope 1 + 2 total %.4f tCO2e", er.Scope1, er.Scope2, er.Total), true)
		if er.Intensity != "" {
			put(er.Intensity, false)
		}
		put("Factors used:", false)
		wrap := func(t string) {
			for len(t) > 88 {
				cut := strings.LastIndex(t[:88], " ")
				if cut < 20 {
					cut = 88
				}
				put(t[:cut], false)
				t = strings.TrimLeft(t[cut:], " ")
			}
			put(t, false)
		}
		for _, l := range er.Sources {
			wrap(l)
		}
		wrap(EmissionsNotice)
		put("", false)
	}
	if rr := BuildRollup(d, series); d.Rollup != "" {
		put("Summary by "+RollupTitle(d), true)
		if len(rr) == 0 {
			put("no data in window", false)
		} else {
			gw := 20 // group column width; nested rollups need room for "Outer / Inner"
			if strings.Contains(d.Rollup, ">") {
				gw = 25
			}
			rh := fmt.Sprintf("%-*s %-12s %4s %8s %10s %10s %10s %12s", gw, RollupTitle(d), "point", "dev", "samples", "avg", "min", "max", "sum")
			put(rh, true)
			for _, r := range rr {
				if len(cur) >= perPage {
					flush()
					put(rh, true)
				}
				g := r.Group
				if len(g) > gw {
					g = g[:gw]
				}
				p := r.Point
				if len(p) > 12 {
					p = p[:12]
				}
				put(fmt.Sprintf("%-*s %-12s %4d %8d %10.3f %10.3f %10.3f %12.3f", gw, g, p, r.Devices, r.Samples, r.Avg, r.Min, r.Max, r.Sum), r.Group == TotalLabel)
			}
			put("Each row totals one point across the devices of one group; points are never added to each other.", false)
		}
	}
	flush()
	if len(pages) == 0 {
		pages = [][]line{{{text: "no data"}}}
	}

	esc := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r == '(' || r == ')' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r >= 32 && r < 127:
				b.WriteRune(r)
			default:
				b.WriteByte('?')
			}
		}
		return b.String()
	}
	var out bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	out.WriteString("%PDF-1.4\n")
	// objects: 1 catalog, 2 pages, 3 font, 4 bold font, then per page (page, content)
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 5+i*2))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold /Encoding /WinAnsiEncoding >>")
	for i, pg := range pages {
		var c strings.Builder
		if dark {
			fmt.Fprintf(&c, "0.09 0.10 0.12 rg 0 0 %d %d re f 0.92 g\n", pageW, pageH)
		}
		y := pageH - 42
		for _, l := range pg {
			if l.chart != nil {
				chartOpsKind(d.Chart, &c, l.chart, l.title, y, esc, dark, float64(pageW)-110)
			}
			f := "F1"
			if l.bold {
				f = "F2"
			}
			if l.text != "" {
				fmt.Fprintf(&c, "BT /%s 9 Tf 40 %d Td (%s) Tj ET\n", f, y, esc(l.text))
			}
			y -= 14
		}
		fmt.Fprintf(&c, "BT /F1 8 Tf 40 24 Td (Page %d of %d) Tj ET\n", i+1, len(pages))
		if d.Header != "" {
			fmt.Fprintf(&c, "BT /F1 8 Tf 40 %d Td (%s) Tj ET\n", pageH-16, esc(d.Header))
		}
		if d.Footer != "" {
			fmt.Fprintf(&c, "BT /F1 8 Tf 140 24 Td (%s) Tj ET\n", esc(d.Footer))
		}
		xo := ""
		if i == 0 && logo != nil {
			w, h := logoSize(logo)
			fmt.Fprintf(&c, "q %.1f 0 0 %.1f %.1f %d cm /Im1 Do Q\n", w, h, float64(pageW-40)-w, pageH-32)
			xo = fmt.Sprintf(" /XObject << /Im1 %d 0 R >>", 5+2*len(pages))
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 3 0 R /F2 4 0 R >>%s >> /Contents %d 0 R >>", pageW, pageH, xo, 6+i*2))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", c.Len(), c.String()))
	}
	if logo != nil {
		pix, pw, ph := logoRGB(logo)
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write(pix)
		zw.Close()
		obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", pw, ph, z.Len(), z.String()))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

const chartSlots = 11

// chartOps draws a line chart of one metric's buckets with PDF vector operators:
// avg as a solid line, min and max as thin grey lines, value range on the left,
// first and last bucket time underneath. y is the baseline of the first reserved
// text line; the chart fills the following chartSlots lines. No fonts or images.
func chartOps(c *strings.Builder, rows []Bucket, title string, y int, esc func(string) string, dark bool, w float64) {
	frame, band, avg := "0.6 G", "0.75 G", "0 G"
	if dark {
		frame, band, avg = "0.45 G", "0.4 G", "0.45 0.7 1 RG"
	}
	const x0, h = 70.0, 100.0
	top := float64(y) - 4
	bottom := top - h - 14 // room above for the title
	plotTop := top - 12
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range rows {
		for _, v := range []float64{r.Min, r.Max, r.Avg} {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	if math.IsInf(lo, 1) {
		return
	}
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	py := func(v float64) float64 { return bottom + (v-lo)/(hi-lo)*(plotTop-bottom) }
	px := func(i int) float64 {
		if len(rows) == 1 {
			return x0 + w/2
		}
		return x0 + float64(i)/float64(len(rows)-1)*w
	}
	fmt.Fprintf(c, "BT /F2 8 Tf %.0f %.1f Td (%s) Tj ET\n", x0, top-8, esc(title+"  (avg, min, max)"))
	fmt.Fprintf(c, "%s 0.5 w %.1f %.1f %.1f %.1f re S\n", frame, x0, bottom, w, plotTop-bottom)
	fmt.Fprintf(c, "BT /F1 7 Tf 40 %.1f Td (%s) Tj ET\n", plotTop-6, esc(fmt.Sprintf("%.4g", hi)))
	fmt.Fprintf(c, "BT /F1 7 Tf 40 %.1f Td (%s) Tj ET\n", bottom, esc(fmt.Sprintf("%.4g", lo)))
	fmt.Fprintf(c, "BT /F1 7 Tf %.0f %.1f Td (%s) Tj ET\n", x0, bottom-8, esc(rows[0].Start.UTC().Format("2006-01-02 15:04")))
	fmt.Fprintf(c, "BT /F1 7 Tf %.0f %.1f Td (%s) Tj ET\n", x0+w-80, bottom-8, esc(rows[len(rows)-1].Start.UTC().Format("2006-01-02 15:04")))
	// at most ~240 points per line: keep every n-th bucket for long windows
	step := 1
	if len(rows) > 240 {
		step = (len(rows) + 239) / 240
	}
	series := func(get func(Bucket) float64, gray string, width float64) {
		fmt.Fprintf(c, "%s %.1f w\n", gray, width)
		first := true
		for i := 0; i < len(rows); i += step {
			v := get(rows[i])
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			if first {
				fmt.Fprintf(c, "%.1f %.1f m\n", px(i), py(v))
				first = false
			} else {
				fmt.Fprintf(c, "%.1f %.1f l\n", px(i), py(v))
			}
		}
		if len(rows) == 1 {
			fmt.Fprintf(c, "%.1f %.1f l\n", px(0)+1, py(get(rows[0])))
		}
		c.WriteString("S\n")
	}
	series(func(b Bucket) float64 { return b.Max }, band, 0.5)
	series(func(b Bucket) float64 { return b.Min }, band, 0.5)
	series(func(b Bucket) float64 { return b.Avg }, avg, 1.2)
	if dark {
		c.WriteString("0.92 g 0.92 G 1 w\n")
	} else {
		c.WriteString("0 G 1 w\n")
	}
}

// logoSize returns the drawn size in points: at most 90 wide and 28 high, aspect kept.
func logoSize(img image.Image) (float64, float64) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	if w <= 0 || h <= 0 {
		return 1, 1
	}
	k := math.Min(90/w, 28/h)
	return w * k, h * k
}

// logoRGB samples the image down to at most 240 pixels wide (nearest neighbour) and
// composites transparency over white.
func logoRGB(img image.Image) ([]byte, int, int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	step := 1
	for w/step > 240 {
		step++
	}
	ow, oh := (w+step-1)/step, (h+step-1)/step
	out := make([]byte, 0, ow*oh*3)
	for y := 0; y < oh; y++ {
		for x := 0; x < ow; x++ {
			r, g, bl, a := img.At(b.Min.X+x*step, b.Min.Y+y*step).RGBA()
			// premultiplied 16-bit values: add white for the missing coverage
			r, g, bl = r+(0xffff-a), g+(0xffff-a), bl+(0xffff-a)
			out = append(out, byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}
	return out, ow, oh
}
