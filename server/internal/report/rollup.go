package report

import (
	"fmt"
	"sort"
)

// Rollup groups metrics by the asset or site their device belongs to and
// totals each point across the group. Rows are grouped per point_id on purpose:
// adding kWh to degrees would be meaningless, so every subtotal is one point
// across the devices of one group, and the grand total is one point across all
// groups. Devices with no asset (or site) go under UnassignedLabel.
const UnassignedLabel = "(unassigned)"
const TotalLabel = "Total (all groups)"

type RollupRow struct {
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
	r := RollupRow{Group: group, Point: point, Devices: len(a.devs), Samples: a.n, Min: a.min, Max: a.max, Sum: a.sum}
	if a.n > 0 {
		r.Avg = a.sum / float64(a.n)
	}
	return r
}

// BuildRollup returns subtotal rows (sorted by group then point) followed by
// grand-total rows (sorted by point). Empty when d.Rollup is off or no metric has data.
func BuildRollup(d Definition, series map[Metric][]Bucket) []RollupRow {
	if d.Rollup == "" {
		return nil
	}
	type key struct{ group, point string }
	groups := map[key]*acc{}
	totals := map[string]*acc{}
	for _, m := range d.Metrics {
		rows := series[m]
		if len(rows) == 0 {
			continue
		}
		g := d.GroupLabels[m.DeviceID]
		if g == "" {
			g = UnassignedLabel
		}
		k := key{g, m.PointID}
		if groups[k] == nil {
			groups[k] = &acc{}
		}
		if totals[m.PointID] == nil {
			totals[m.PointID] = &acc{}
		}
		groups[k].add(m.DeviceID, rows)
		totals[m.PointID].add(m.DeviceID, rows)
	}
	var out []RollupRow
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].group != keys[j].group {
			if keys[i].group == UnassignedLabel || keys[j].group == UnassignedLabel {
				return keys[j].group == UnassignedLabel
			}
			return keys[i].group < keys[j].group
		}
		return keys[i].point < keys[j].point
	})
	for _, k := range keys {
		out = append(out, groups[k].row(k.group, k.point))
	}
	pts := make([]string, 0, len(totals))
	for p := range totals {
		pts = append(pts, p)
	}
	sort.Strings(pts)
	for _, p := range pts {
		out = append(out, totals[p].row(TotalLabel, p))
	}
	return out
}

// RollupHeader names the columns of RollupCells.
func RollupHeader(d Definition) []string {
	return []string{d.Rollup, "point", "devices", "samples", "avg", "min", "max", "sum"}
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
