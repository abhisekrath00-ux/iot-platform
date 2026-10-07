package report

import (
	"bytes"
	"encoding/xml"
	"math"
	"strconv"
	"time"
)

// RenderXML writes the report data as XML, one <series> per metric with one <bucket> per row.
// encoding/xml escapes all text; non-finite numbers are written as empty attributes so the
// document is always well formed. Rollups and matrix layout are not in the XML (flat series only).
func RenderXML(title string, d Definition, series map[Metric][]Bucket, generated time.Time) ([]byte, error) {
	num := func(v float64) string {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ""
		}
		return strconv.FormatFloat(v, 'g', 6, 64)
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	e := xml.NewEncoder(&buf)
	e.Indent("", "  ")
	start := func(name string, attrs ...xml.Attr) error {
		return e.EncodeToken(xml.StartElement{Name: xml.Name{Local: name}, Attr: attrs})
	}
	end := func(name string) error { return e.EncodeToken(xml.EndElement{Name: xml.Name{Local: name}}) }
	a := func(k, v string) xml.Attr { return xml.Attr{Name: xml.Name{Local: k}, Value: v} }
	if err := start("report", a("title", title), a("generated", generated.UTC().Format(time.RFC3339)), a("group_by", d.GroupBy), a("window_hours", strconv.Itoa(d.WindowHours))); err != nil {
		return nil, err
	}
	for _, m := range d.Metrics {
		if err := start("series", a("device_id", m.DeviceID), a("point_id", m.PointID)); err != nil {
			return nil, err
		}
		for _, k := range series[m] {
			if err := start("bucket", a("start", k.Start.UTC().Format(time.RFC3339)), a("avg", num(k.Avg)), a("min", num(k.Min)), a("max", num(k.Max)), a("sum", num(k.Sum)), a("count", strconv.Itoa(k.Count))); err != nil {
				return nil, err
			}
			if err := end("bucket"); err != nil {
				return nil, err
			}
		}
		if err := end("series"); err != nil {
			return nil, err
		}
	}
	if err := end("report"); err != nil {
		return nil, err
	}
	if err := e.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
