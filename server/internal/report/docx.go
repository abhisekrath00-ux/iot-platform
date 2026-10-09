package report

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// RenderDOCX writes the report as a Word document using only the standard library (no dependency,
// works air-gapped). Title, generated line, one table per metric (or the matrix table), the summary
// grouping table, header and footer text, page numbers, and the PDF page size and orientation.
// Cells hold plain text runs only: no fields from data, no macros, no external links. The logo
// and the insights section are not in the Word file.
func RenderDOCX(title string, d Definition, series map[Metric][]Bucket, generated time.Time) ([]byte, error) {
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	run := func(s string, bold bool, extra string) string {
		rpr := ""
		if bold || extra != "" {
			rpr = "<w:rPr>" + map[bool]string{true: "<w:b/>", false: ""}[bold] + extra + "</w:rPr>"
		}
		return `<w:r>` + rpr + `<w:t xml:space="preserve">` + esc(s) + `</w:t></w:r>`
	}
	para := func(style, s string) string {
		ps := ""
		if style != "" {
			ps = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
		}
		return `<w:p>` + ps + run(s, false, "") + `</w:p>`
	}
	type cell struct {
		text  string
		fill  string
		right bool
	}
	table := func(hdr []string, rows [][]cell, boldLast bool) string {
		var b strings.Builder
		b.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/></w:tblPr><w:tblGrid>`)
		for range hdr {
			b.WriteString(`<w:gridCol/>`)
		}
		b.WriteString(`</w:tblGrid><w:tr><w:trPr><w:tblHeader/></w:trPr>`)
		for _, h := range hdr {
			b.WriteString(`<w:tc><w:tcPr><w:shd w:val="clear" w:color="auto" w:fill="E5E7EB"/></w:tcPr><w:p>` + run(h, true, "") + `</w:p></w:tc>`)
		}
		b.WriteString(`</w:tr>`)
		for i, r := range rows {
			b.WriteString(`<w:tr>`)
			for _, c := range r {
				tcpr := ""
				if c.fill != "" {
					tcpr = `<w:tcPr><w:shd w:val="clear" w:color="auto" w:fill="` + c.fill + `"/></w:tcPr>`
				}
				ppr := ""
				if c.right {
					ppr = `<w:pPr><w:jc w:val="right"/></w:pPr>`
				}
				b.WriteString(`<w:tc>` + tcpr + `<w:p>` + ppr + run(c.text, boldLast && i == len(rows)-1, "") + `</w:p></w:tc>`)
			}
			b.WriteString(`</w:tr>`)
		}
		b.WriteString(`</w:tbl>` + para("", ""))
		return b.String()
	}
	plain := func(ss []string) []cell {
		out := make([]cell, len(ss))
		for i, s := range ss {
			out[i] = cell{text: s, right: i > 0}
		}
		return out
	}
	w, h := PageSize(d.Page)
	var pics [][]byte // PNG charts, word/media/chartN.png with relationship rId(N+3)
	chartPara := func(name string, rows []Bucket) string {
		if len(pics) >= maxXLSXCharts {
			return ""
		}
		img, caption := ChartPNG(d.Chart, rows)
		if img == nil {
			return ""
		}
		pics = append(pics, img)
		n := len(pics)
		cx := int64(w*20-2268) * 635 // text width in EMU: twips * 635
		cy := cx * pngH / pngW
		var alt bytes.Buffer
		xml.EscapeText(&alt, []byte(name+" chart"))
		drawing := fmt.Sprintf(`<w:p><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Chart %d" descr="%s"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:nvPicPr><pic:cNvPr id="0" name="chart%d.png"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="rId%d"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`, cx, cy, n, n, alt.String(), n, n+3, cx, cy)
		return drawing + para("", caption)
	}
	var body strings.Builder
	body.WriteString(para("Title", title))
	body.WriteString(para("", fmt.Sprintf("Generated %s - window %dh, grouped by %s", generated.UTC().Format("2006-01-02T15:04:05Z"), d.WindowHours, d.GroupBy)))
	if d.Layout == "matrix" {
		hdr, rows, total := Matrix(d, series)
		var cs [][]cell
		for _, r := range append(rows, total) {
			cs = append(cs, plain(r))
		}
		for i, m := range d.Metrics {
			if i >= 4 {
				break
			}
			if c := chartPara(m.DeviceID+" / "+m.PointID, series[m]); c != "" {
				body.WriteString(para("Heading2", m.DeviceID+" / "+m.PointID) + c)
			}
		}
		body.WriteString(table(append([]string{d.GroupBy}, hdr...), cs, true))
	} else {
		for _, m := range d.Metrics {
			rows := series[m]
			body.WriteString(para("Heading2", m.DeviceID+" / "+m.PointID))
			if len(rows) == 0 {
				body.WriteString(para("", "no data in window"))
				continue
			}
			body.WriteString(chartPara(m.DeviceID+" / "+m.PointID, rows))
			var cs [][]cell
			for _, k := range rows {
				fill := d.Highlight.fill(k)
				cs = append(cs, []cell{{text: k.Start.UTC().Format("2006-01-02 15:04")}, {text: fmt.Sprintf("%.3f", k.Avg), fill: fill, right: true},
					{text: fmt.Sprintf("%.3f", k.Min), right: true}, {text: fmt.Sprintf("%.3f", k.Max), right: true},
					{text: fmt.Sprintf("%.3f", k.Sum), right: true}, {text: fmt.Sprint(k.Count), right: true}})
			}
			a, mn, mx, sm, cnt := Summary(rows)
			cs = append(cs, plain([]string{"overall", fmt.Sprintf("%.3f", a), fmt.Sprintf("%.3f", mn), fmt.Sprintf("%.3f", mx), fmt.Sprintf("%.3f", sm), fmt.Sprint(cnt)}))
			body.WriteString(table([]string{"bucket (UTC)", "avg", "min", "max", "sum", "samples"}, cs, true))
		}
	}
	for _, sc := range d.allSections() {
		body.WriteString(para("Heading2", strings.TrimSpace(sc.Title)))
		for _, p := range sectionParas(sc.Body) {
			body.WriteString(para("", p))
		}
	}
	if len(d.Emissions) > 0 {
		er := BuildEmissions(d, series)
		body.WriteString(para("Heading2", "Emissions (Scope 1 and 2)"))
		var cs [][]cell
		for _, r := range EmissionsCells(er) {
			cs = append(cs, plain(r))
		}
		body.WriteString(table(EmissionsHeader(), cs, false))
		if er.Intensity != "" {
			body.WriteString(para("", er.Intensity))
		}
		body.WriteString(para("", "Factors used:"))
		for _, l := range er.Sources {
			body.WriteString(para("", l))
		}
		body.WriteString(para("", EmissionsNotice))
	}
	if d.Rollup != "" {
		body.WriteString(para("Heading2", "Summary by "+RollupTitle(d)))
		rr := BuildRollup(d, series)
		if len(rr) == 0 {
			body.WriteString(para("", "no data in window"))
		} else {
			var cs [][]cell
			for _, r := range RollupCells(rr) {
				c := plain(r)
				c[1].right = false // the point name reads left-aligned
				cs = append(cs, c)
			}
			body.WriteString(table(RollupHeader(d), cs, false))
		}
	}
	orient := ""
	if w > h {
		orient = ` w:orient="landscape"`
	}
	sect := fmt.Sprintf(`<w:sectPr><w:headerReference w:type="default" r:id="rId2"/><w:footerReference w:type="default" r:id="rId3"/><w:pgSz w:w="%d" w:h="%d"%s/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="567" w:footer="567" w:gutter="0"/></w:sectPr>`, w*20, h*20, orient)
	const ns = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"`
	const hdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	files := []struct{ n, b string }{
		{"[Content_Types].xml", hdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/><Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/></Types>`},
		{"_rels/.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/_rels/document.xml.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>` + docxImageRels(len(pics)) + `</Relationships>`},
		{"word/styles.xml", hdr + `<w:styles ` + ns + `><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="20"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="120"/></w:pPr><w:rPr><w:b/><w:sz w:val="40"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style><w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:color="BBBBBB"/><w:left w:val="single" w:sz="4" w:color="BBBBBB"/><w:bottom w:val="single" w:sz="4" w:color="BBBBBB"/><w:right w:val="single" w:sz="4" w:color="BBBBBB"/><w:insideH w:val="single" w:sz="4" w:color="BBBBBB"/><w:insideV w:val="single" w:sz="4" w:color="BBBBBB"/></w:tblBorders><w:tblCellMar><w:left w:w="80" w:type="dxa"/><w:right w:w="80" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style></w:styles>`},
		{"word/header1.xml", hdr + `<w:hdr ` + ns + `><w:p>` + run(d.Header, false, "") + `</w:p></w:hdr>`},
		{"word/footer1.xml", hdr + `<w:ftr ` + ns + `><w:p>` + run(d.Footer+"  Page ", false, "") + `<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> PAGE </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>1</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p></w:ftr>`},
		{"word/document.xml", hdr + `<w:document ` + ns + `><w:body>` + body.String() + sect + `</w:body></w:document>`},
	}
	for i, p := range pics {
		files = append(files, struct{ n, b string }{fmt.Sprintf("word/media/chart%d.png", i+1), string(p)})
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		wr, err := zw.Create(f.n)
		if err != nil {
			return nil, err
		}
		if _, err := wr.Write([]byte(f.b)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func docxImageRels(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/chart%d.png"/>`, i+3, i)
	}
	return b.String()
}
