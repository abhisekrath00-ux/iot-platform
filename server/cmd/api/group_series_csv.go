package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Text goes through formula neutralization before RFC4180 quoting. Do not
// change numeric signs: negative physical values remain numeric cells.
func groupCSVText(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	trim := strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	if len(trim) > 0 && strings.ContainsRune("=+-@", rune(trim[0])) {
		return "'" + s
	}
	return s
}
func writeGroupSeriesCSV(w http.ResponseWriter, group, point, agg string, rows []map[string]any) {
	for _, r := range rows {
		if r["units_mixed"] == true {
			http.Error(w, "mixed units in the window; CSV withheld", http.StatusConflict)
			return
		}
	}
	var b bytes.Buffer
	c := csv.NewWriter(&b)
	c.UseCRLF = true
	c.Write([]string{"group_id", "point_id", "aggregate", "window_hours", "bucket_utc", "value", "unit", "contributing_devices", "current_scoped_members", "raw_samples", "membership_basis", "data_basis"})
	for _, r := range rows {
		value := ""
		if v, ok := r["v"].(*float64); ok && v != nil {
			value = strconv.FormatFloat(*v, 'g', -1, 64)
		}
		c.Write([]string{groupCSVText(group), groupCSVText(point), agg, "24", r["t"].(time.Time).UTC().Format(time.RFC3339), value, groupCSVText(r["unit"].(string)), fmt.Sprint(r["devices"]), fmt.Sprint(r["members"]), fmt.Sprint(r["samples"]), "current transitive group and customer scope, not historical membership", "finite measured/estimated raw samples; device hourly means then group aggregate; missing devices omitted; first/current hours may be partial"})
	}
	c.Flush()
	if c.Error() != nil {
		http.Error(w, "CSV generation failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="group-series-24h.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	w.Write(b.Bytes())
}
