package report

import (
	"fmt"
	"sort"
	"strings"
)

// Rollup groups metrics by the asset or site their device belongs to and
// totals each point across the group. Rows are grouped per point_id on purpose:
// adding kWh to degrees would be meaningless, so every subtotal is one point
// across the devices of one group, and the grand total is one point across all
// groups. Devices with no asset (or site) go under UnassignedLabel.
const UnassignedLabel = "(unassigned)"
const TotalLabel = "Total (all groups)"

type RollupRow struct {
	Level   int // 0 grand total, 1 subtotal of an outer group (nested rollups), 2 leaf group
	Group   string
	Point   string
	Devices int // devices that had data for this point
	Samples int
	Avg     float64
	Min     float64
	Max     float64
	Sum     float64
}

type acc struct {
	devs map[string]bool
	n    int
	sum  float64
	min  float64
	max  float64
	set  bool
}

func (a *acc) add(dev string, rows []Bucket) {
	for _, r := range rows {
		if r.Count == 0 {
			continue
		}
		if a.devs == nil {
			a.devs = map[string]bool{}
		}
		a.devs[dev] = true
		a.n += r.Count
		a.sum += r.Sum
		if !a.set || r.Min < a.min {
			a.min = r.Min
		}
		if !a.set || r.Max > a.max {
			a.max = r.Max
		}
		a.set = true
	}
}

func (a *acc) row(group, point string) RollupRow {
	r := RollupRow{Level: 2, Group: group, Point: point, Devices: len(a.devs), Samples: a.n, Min: a.min, Max: a.max, Sum: a.sum}
	if a.n > 0 {
		r.Avg = a.sum / float64(a.n)
	}
	return r
}

// RollupLevels splits a rollup setting into its outer and inner group kinds.
// "asset" or "site" gives one level; "site>asset" or "asset>site" nests the second inside the first.
func RollupLevels(r string) (outer, inner string) {
	if i := strings.Index(r, ">"); i > 0 {
		return r[:i], r[i+1:]
	}
	return r, ""
}

// RollupTitle is the rollup setting as shown to readers ("site > asset").
func RollupTitle(d Definition) string { return strings.ReplaceAll(d.Rollup, ">", " > ") }

func labelOf(m map[string]string, dev string) string {
	if g := m[dev]; g != "" {
		return g
	}
	return UnassignedLabel
}

// BuildRollup returns group rows followed by grand-total rows (sorted by point).
// A nested rollup ("site>asset") gives, per outer group, its subtotal rows
// (Level 1) followed by one row per inner group named "Outer / Inner" (Level 2).
// Empty when d.Rollup is off or no metric has data.
func BuildRollup(d Definition, series map[Metric][]Bucket) []RollupRow {
	if d.Rollup == "" {
		return nil
	}
	_, inner := RollupLevels(d.Rollup)
	type key struct{ outer, inner, point string }
	leaf := map[key]*acc{}
	outerAcc := map[key]*acc{}
	totals := map[string]*acc{}
	get := func(m map[key]*acc, k key) *acc {
		if m[k] == nil {
			m[k] = &acc{}
		}
		return m[k]
	}
	for _, m := range d.Metrics {
		rows := series[m]
		if len(rows) == 0 {
			continue
		}
		o := labelOf(d.GroupLabels, m.DeviceID)
		in := ""
		if inner != "" {
			in = labelOf(d.InnerLabels, m.DeviceID)
		}
		if totals[m.PointID] == nil {
			totals[m.PointID] = &acc{}
		}
		get(leaf, key{o, in, m.PointID}).add(m.DeviceID, rows)
		if inner != "" {
			get(outerAcc, key{o, "", m.PointID}).add(m.DeviceID, rows)
		}
		totals[m.PointID].add(m.DeviceID, rows)
	}
	less := func(a, b string) bool {
		if a != b && (a == UnassignedLabel || b == UnassignedLabel) {
			return b == UnassignedLabel
		}
		return a < b
	}
	sorted := func(m map[key]*acc) []key {
		keys := make([]key, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].outer != keys[j].outer {
				return less(keys[i].outer, keys[j].outer)
			}
			if keys[i].inner != keys[j].inner {
				return less(keys[i].inner, keys[j].inner)
			}
			return keys[i].point < keys[j].point
		})
		return keys
	}
	var out []RollupRow
	if inner == "" {
		for _, k := range sorted(leaf) {
			out = append(out, leaf[k].row(k.outer, k.point))
		}
	} else {
		ok := sorted(outerAcc)
		lk := sorted(leaf)
		for i := 0; i < len(ok); {
			o := ok[i].outer
			for ; i < len(ok) && ok[i].outer == o; i++ {
				r := outerAcc[ok[i]].row(o, ok[i].point)
				r.Level = 1
				out = append(out, r)
			}
			for _, k := range lk {
				if k.outer == o {
					out = append(out, leaf[k].row(o+" / "+k.inner, k.point))
				}
			}
		}
	}
	pts := make([]string, 0, len(totals))
	for p := range totals {
		pts = append(pts, p)
	}
	sort.Strings(pts)
	for _, p := range pts {
		r := totals[p].row(TotalLabel, p)
		r.Level = 0
		out = append(out, r)
	}
	return out
}

// RollupHeader names the columns of RollupCells.
func RollupHeader(d Definition) []string {
	return []string{RollupTitle(d), "point", "devices", "samples", "avg", "min", "max", "sum"}
}

// RollupCells formats rows as strings (3 decimals) for HTML, CSV, XLSX and PDF.
func RollupCells(rows []RollupRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Group, r.Point, fmt.Sprint(r.Devices), fmt.Sprint(r.Samples),
			fmt.Sprintf("%.3f", r.Avg), fmt.Sprintf("%.3f", r.Min), fmt.Sprintf("%.3f", r.Max), fmt.Sprintf("%.3f", r.Sum)})
	}
	return out
}
