package report

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const (
	pptxRowsPerSlide = 12
	pptxMaxSlides    = 60
)

// RenderPPTX writes the report as a PowerPoint deck using only the standard library (no dependency,
// works air-gapped). Slide 1 is the title; each metric gets table slides of at most 12 rows (with an
// overall row on the last one); the summary grouping gets its own slides. Text only: no macros, media or
// external links. The logo and the insights section are not in the deck, and a report that
// would need more than 60 slides is cut off with a final note slide.
func RenderPPTX(title string, d Definition, series map[Metric][]Bucket, generated time.Time) ([]byte, error) {
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	const (
		slideW = 12192000
		slideH = 6858000
	)
	textBox := func(id int, name, text string, x, y, w, h, size int, bold bool) string {
		b := ""
		if bold {
			b = ` b="1"`
		}
		return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr wrap="square"/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="%d"%s/><a:t>%s</a:t></a:r></a:p></p:txBody></p:sp>`,
			id, name, x, y, w, h, size, b, esc(text))
	}
	type cell struct{ text, fill string }
	tableFrame := func(hdr []string, rows [][]cell, boldLast bool) string {
		if len(hdr) == 0 {
			return ""
		}
		colW := (slideW - 2*457200) / len(hdr)
		rowH := 320000
		var b strings.Builder
		fmt.Fprintf(&b, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="4" name="Table"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr><p:xfrm><a:off x="457200" y="1300000"/><a:ext cx="%d" cy="%d"/></p:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1"/><a:tblGrid>`, colW*len(hdr), rowH*(len(rows)+1))
		for range hdr {
			fmt.Fprintf(&b, `<a:gridCol w="%d"/>`, colW)
		}
		b.WriteString(`</a:tblGrid>`)
		tc := func(text, fill string, bold bool) string {
			bb := ""
			if bold {
				bb = ` b="1"`
			}
			if fill == "" {
				fill = "FFFFFF"
			}
			return fmt.Sprintf(`<a:tc><a:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="1200"%s><a:solidFill><a:srgbClr val="111111"/></a:solidFill></a:rPr><a:t>%s</a:t></a:r></a:p></a:txBody><a:tcPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:tcPr></a:tc>`, bb, esc(text), fill)
		}
		fmt.Fprintf(&b, `<a:tr h="%d">`, rowH)
		for _, h := range hdr {
			b.WriteString(tc(h, "E5E7EB", true))
		}
		b.WriteString(`</a:tr>`)
		for i, r := range rows {
			fmt.Fprintf(&b, `<a:tr h="%d">`, rowH)
			for _, c := range r {
				b.WriteString(tc(c.text, c.fill, boldLast && i == len(rows)-1))
			}
			b.WriteString(`</a:tr>`)
		}
		b.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
		return b.String()
	}
	plain := func(ss []string) []cell {
		out := make([]cell, len(ss))
		for i, s := range ss {
			out[i] = cell{text: s}
		}
		return out
	}
	var slides []string
	add := func(shapes string) bool {
		if len(slides) >= pptxMaxSlides {
			return false
		}
		slides = append(slides, shapes)
		return true
	}
	add(textBox(2, "Title", title, 457200, 2200000, slideW-914400, 900000, 4000, true) +
		textBox(3, "Subtitle", fmt.Sprintf("Generated %s - window %dh, grouped by %s", generated.UTC().Format("2006-01-02T15:04:05Z"), d.WindowHours, d.GroupBy), 457200, 3200000, slideW-914400, 500000, 1600, false))
	var pics [][]byte
	slidePic := map[int]int{} // slide index -> picture number (1-based)
	chartSlide := func(name string, rows []Bucket) bool {
		if len(pics) >= maxXLSXCharts {
			return true
		}
		img, caption := ChartPNG(d.Chart, rows)
		if img == nil {
			return true
		}
		pics = append(pics, img)
		w := slideW - 914400
		pic := fmt.Sprintf(`<p:pic><p:nvPicPr><p:cNvPr id="5" name="Chart" descr="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="rId2"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr><a:xfrm><a:off x="457200" y="1100000"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`, esc(name+" chart"), w, w*pngH/pngW)
		if !add(textBox(2, "Title", name, 457200, 300000, slideW-914400, 700000, 2800, true) + pic + textBox(3, "Caption", caption, 457200, 5300000, slideW-914400, 700000, 1400, false)) {
			return false
		}
		slidePic[len(slides)-1] = len(pics)
		return true
	}
	paged := func(head string, hdr []string, rows [][]cell, boldLast bool) bool {
		if len(rows) == 0 {
			return add(textBox(2, "Title", head, 457200, 300000, slideW-914400, 700000, 2800, true) + textBox(3, "Note", "no data in window", 457200, 1300000, slideW-914400, 400000, 1600, false))
		}
		pages := (len(rows) + pptxRowsPerSlide - 1) / pptxRowsPerSlide
		for p := 0; p < pages; p++ {
			lo, hi := p*pptxRowsPerSlide, (p+1)*pptxRowsPerSlide
			if hi > len(rows) {
				hi = len(rows)
			}
			t := head
			if pages > 1 {
				t = fmt.Sprintf("%s (%d/%d)", head, p+1, pages)
			}
			if !add(textBox(2, "Title", t, 457200, 300000, slideW-914400, 700000, 2800, true) + tableFrame(hdr, rows[lo:hi], boldLast && p == pages-1)) {
				return false
			}
		}
		return true
	}
	ok := true
	if d.Layout == "matrix" {
		hdr, rows, total := Matrix(d, series)
		var cs [][]cell
		for _, r := range append(rows, total) {
			cs = append(cs, plain(r))
		}
		for i, m := range d.Metrics {
			if i >= 4 || !ok {
				break
			}
			ok = chartSlide(m.DeviceID+" / "+m.PointID, series[m])
		}
		if ok {
			ok = paged("Matrix", append([]string{d.GroupBy}, hdr...), cs, true)
		}
	} else {
		for _, m := range d.Metrics {
			rows := series[m]
			var cs [][]cell
			for _, k := range rows {
				fill := d.Highlight.fill(k)
				cs = append(cs, []cell{{text: k.Start.UTC().Format("2006-01-02 15:04")}, {text: fmt.Sprintf("%.3f", k.Avg), fill: fill},
					{text: fmt.Sprintf("%.3f", k.Min)}, {text: fmt.Sprintf("%.3f", k.Max)}, {text: fmt.Sprintf("%.3f", k.Sum)}, {text: fmt.Sprint(k.Count)}})
			}
			if len(rows) > 0 {
				a, mn, mx, sm, cnt := Summary(rows)
				cs = append(cs, plain([]string{"overall", fmt.Sprintf("%.3f", a), fmt.Sprintf("%.3f", mn), fmt.Sprintf("%.3f", mx), fmt.Sprintf("%.3f", sm), fmt.Sprint(cnt)}))
			}
			if ok = chartSlide(m.DeviceID+" / "+m.PointID, rows); !ok {
				break
			}
			if ok = paged(m.DeviceID+" / "+m.PointID, []string{"bucket (UTC)", "avg", "min", "max", "sum", "samples"}, cs, true); !ok {
				break
			}
		}
	}
	for _, sc := range d.Sections {
		if !ok {
			break
		}
		var ns [][]cell
		for _, p := range sectionParas(sc.Body) {
			ns = append(ns, plain([]string{p}))
		}
		ok = paged(strings.TrimSpace(sc.Title), []string{"note"}, ns, false)
	}
	if ok && len(d.Emissions) > 0 {
		er := BuildEmissions(d, series)
		var cs [][]cell
		for _, r := range EmissionsCells(er) {
			cs = append(cs, plain(r))
		}
		ok = paged("Emissions (Scope 1 and 2)", EmissionsHeader(), cs, false)
		if ok {
			notes := append([]string{"Factors used:"}, er.Sources...)
			if er.Intensity != "" {
				notes = append([]string{er.Intensity}, notes...)
			}
			notes = append(notes, EmissionsNotice)
			var ns [][]cell
			for _, l := range notes {
				ns = append(ns, plain([]string{l}))
			}
			ok = paged("Emissions: factors and notice", []string{"note"}, ns, false)
		}
	}
	if ok && d.Rollup != "" {
		var cs [][]cell
		for _, r := range RollupCells(BuildRollup(d, series)) {
			cs = append(cs, plain(r))
		}
		ok = paged("Summary by "+RollupTitle(d), RollupHeader(d), cs, false)
	}
	if !ok {
		// the cap was hit: replace the last slide with a note so nothing is silently missing
		slides[len(slides)-1] = textBox(2, "Title", "Report truncated", 457200, 300000, slideW-914400, 700000, 2800, true) +
			textBox(3, "Note", fmt.Sprintf("This deck is limited to %d slides. Download the Excel or CSV export for the full data.", pptxMaxSlides), 457200, 1300000, slideW-914400, 600000, 1600, false)
	}

	const hdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	const nsA = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
	rel := func(items ...string) string {
		return hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + strings.Join(items, "") + `</Relationships>`
	}
	rl := func(id, typ, target string) string {
		return fmt.Sprintf(`<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/%s" Target="%s"/>`, id, typ, target)
	}
	ct := hdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`
	var sldIds, presRels strings.Builder
	presRels.WriteString(rl("rId1", "slideMaster", "slideMasters/slideMaster1.xml") + rl("rId2", "theme", "theme/theme1.xml"))
	for i := range slides {
		ct += fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1)
		fmt.Fprintf(&sldIds, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, 10+i)
		presRels.WriteString(rl(fmt.Sprintf("rId%d", 10+i), "slide", fmt.Sprintf("slides/slide%d.xml", i+1)))
	}
	ct += `</Types>`
	grp := `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`
	files := []struct{ n, b string }{
		{"[Content_Types].xml", ct},
		{"_rels/.rels", rel(rl("rId1", "officeDocument", "ppt/presentation.xml"))},
		{"ppt/presentation.xml", hdr + `<p:presentation ` + nsA + `><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst>` + sldIds.String() + fmt.Sprintf(`</p:sldIdLst><p:sldSz cx="%d" cy="%d"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`, slideW, slideH)},
		{"ppt/_rels/presentation.xml.rels", rel(presRels.String())},
		{"ppt/slideMasters/slideMaster1.xml", hdr + `<p:sldMaster ` + nsA + `><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree>` + grp + `</p:spTree></p:cSld><p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/><p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>`},
		{"ppt/slideMasters/_rels/slideMaster1.xml.rels", rel(rl("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"), rl("rId2", "theme", "../theme/theme1.xml"))},
		{"ppt/slideLayouts/slideLayout1.xml", hdr + `<p:sldLayout ` + nsA + ` type="blank" preserve="1"><p:cSld name="Blank"><p:spTree>` + grp + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`},
		{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", rel(rl("rId1", "slideMaster", "../slideMasters/slideMaster1.xml"))},
		{"ppt/theme/theme1.xml", hdr + pptxTheme},
	}
	for i, sh := range slides {
		files = append(files,
			struct{ n, b string }{fmt.Sprintf("ppt/slides/slide%d.xml", i+1), hdr + `<p:sld ` + nsA + `><p:cSld><p:spTree>` + grp + sh + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`},
			struct{ n, b string }{fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), rel(rl("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml") + slideImageRel(slidePic[i]))})
	}
	for i, p := range pics {
		files = append(files, struct{ n, b string }{fmt.Sprintf("ppt/media/chart%d.png", i+1), string(p)})
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.n)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.b)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const pptxTheme = `<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="HexThings"><a:themeElements><a:clrScheme name="HexThings"><a:dk1><a:srgbClr val="111111"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="1F2937"/></a:dk2><a:lt2><a:srgbClr val="F3F4F6"/></a:lt2><a:accent1><a:srgbClr val="2563EB"/></a:accent1><a:accent2><a:srgbClr val="DC2626"/></a:accent2><a:accent3><a:srgbClr val="16A34A"/></a:accent3><a:accent4><a:srgbClr val="D97706"/></a:accent4><a:accent5><a:srgbClr val="7C3AED"/></a:accent5><a:accent6><a:srgbClr val="0891B2"/></a:accent6><a:hlink><a:srgbClr val="2563EB"/></a:hlink><a:folHlink><a:srgbClr val="7C3AED"/></a:folHlink></a:clrScheme><a:fontScheme name="HexThings"><a:majorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont><a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme><a:fmtScheme name="HexThings"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`

func slideImageRel(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/chart%d.png"/>`, n)
}
