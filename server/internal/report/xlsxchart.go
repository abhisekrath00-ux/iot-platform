package report

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// Native Excel charts for the flat layout: one chart per metric (at most maxXLSXCharts), reading
// the avg column of that metric's rows. line, area, bar and scatter map to Excel chart types;
// gauge and pie have no honest native equivalent here and get no chart in XLSX.
const maxXLSXCharts = 8

type xlChart struct {
	Title       string
	First, Last int // worksheet rows holding the metric's buckets
}

func xlChartKind(k string) bool {
	switch k {
	case "line", "area", "bar", "scatter":
		return true
	}
	return false
}

func xlChartXML(kind string, c xlChart) string {
	var t bytes.Buffer
	xml.EscapeText(&t, []byte(c.Title))
	cat := fmt.Sprintf("Report!$C$%d:$C$%d", c.First, c.Last)
	val := fmt.Sprintf("Report!$D$%d:$D$%d", c.First, c.Last)
	ser := fmt.Sprintf(`<c:ser><c:idx val="0"/><c:order val="0"/><c:tx><c:v>avg</c:v></c:tx>%%s<c:cat><c:strRef><c:f>%s</c:f></c:strRef></c:cat><c:val><c:numRef><c:f>%s</c:f></c:numRef></c:val>%%s</c:ser>`, cat, val)
	fill := `<c:spPr><a:solidFill><a:srgbClr val="2563EB"/></a:solidFill></c:spPr><c:invertIfNegative val="0"/>`
	if kind == "area" {
		fill = `<c:spPr><a:solidFill><a:srgbClr val="2563EB"><a:alpha val="45000"/></a:srgbClr></a:solidFill><a:ln w="19050"><a:solidFill><a:srgbClr val="2563EB"/></a:solidFill></a:ln></c:spPr>`
	}
	axes := `<c:axId val="111"/><c:axId val="222"/>`
	var plot string
	switch kind {
	case "bar":
		plot = `<c:barChart><c:barDir val="col"/><c:grouping val="clustered"/><c:varyColors val="0"/>` + fmt.Sprintf(ser, fill, "") + `<c:gapWidth val="60"/>` + axes + `</c:barChart>`
	case "area":
		plot = `<c:areaChart><c:grouping val="standard"/><c:varyColors val="0"/>` + fmt.Sprintf(ser, fill, "") + axes + `</c:areaChart>`
	case "scatter":
		sc := fmt.Sprintf(`<c:ser><c:idx val="0"/><c:order val="0"/><c:tx><c:v>avg</c:v></c:tx><c:spPr><a:ln w="19050"><a:noFill/></a:ln></c:spPr><c:marker><c:symbol val="circle"/><c:size val="5"/></c:marker><c:xVal><c:strRef><c:f>%s</c:f></c:strRef></c:xVal><c:yVal><c:numRef><c:f>%s</c:f></c:numRef></c:yVal></c:ser>`, cat, val)
		plot = `<c:scatterChart><c:scatterStyle val="lineMarker"/>` + sc + axes + `</c:scatterChart>`
	default:
		plot = `<c:lineChart><c:grouping val="standard"/>` + fmt.Sprintf(ser, `<c:spPr><a:ln w="22225"><a:solidFill><a:srgbClr val="2563EB"/></a:solidFill></a:ln></c:spPr><c:marker><c:symbol val="none"/></c:marker>`, `<c:smooth val="0"/>`) + `<c:marker val="1"/>` + axes + `</c:lineChart>`
	}
	catAx := `<c:catAx><c:axId val="111"/><c:scaling><c:orientation val="minMax"/></c:scaling><c:delete val="0"/><c:axPos val="b"/><c:crossAx val="222"/></c:catAx>`
	valAx := `<c:valAx><c:axId val="222"/><c:scaling><c:orientation val="minMax"/></c:scaling><c:delete val="0"/><c:axPos val="l"/><c:majorGridlines/><c:crossAx val="111"/></c:valAx>`
	if kind == "scatter" { // scatter needs two value axes; the x values are bucket labels, so plot by point order
		catAx = `<c:valAx><c:axId val="111"/><c:scaling><c:orientation val="minMax"/></c:scaling><c:delete val="0"/><c:axPos val="b"/><c:crossAx val="222"/></c:valAx>`
	}
	return `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><c:chart><c:title><c:tx><c:rich><a:bodyPr/><a:p><a:r><a:t>` + t.String() + `</a:t></a:r></a:p></c:rich></c:tx><c:overlay val="0"/></c:title><c:autoTitleDeleted val="0"/><c:plotArea><c:layout/>` + plot + catAx + valAx + `</c:plotArea><c:legend><c:legendPos val="b"/></c:legend><c:plotVisOnly val="1"/></c:chart></c:chartSpace>`
}
