package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/kpi"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
)

// GET /v1/reports/check-formula?kind=highlight|kpi|computed&expression=...
// Validates a formula with the same parser the server uses when it saves it, and says what is wrong in plain
// words. Nothing is stored. The assistant calls this before it proposes a report, so a wrong formula is fixed
// before the user is asked to confirm anything.
func (s *server) checkFormula(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	src := r.URL.Query().Get("expression")
	out := map[string]any{"kind": kind, "ok": false}
	e, err := kpi.Parse(src)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}
	refs := make([]string, 0, len(e.Refs))
	for _, x := range e.Refs {
		refs = append(refs, x.String())
	}
	out["refs"] = refs
	switch kind {
	case "highlight":
		d := report.Definition{Metrics: []report.Metric{{DeviceID: "d", PointID: "p"}}, WindowHours: 24, GroupBy: "hour", Highlight: &report.Highlight{When: src}}
		if err := report.Validate(d); err != nil {
			out["error"] = err.Error()
			writeJSON(w, 200, out)
			return
		}
		out["hint"] = "result is non-zero = the cell is coloured; use if(condition, 1, 0)"
	case "computed", "kpi":
		for _, x := range e.Refs {
			if x.Device == "row" {
				out["error"] = "{row.*} fields only work in highlight rules; use {device.point}"
				writeJSON(w, 200, out)
				return
			}
		}
	default:
		out["error"] = "kind must be highlight, kpi or computed"
		writeJSON(w, 200, out)
		return
	}
	out["ok"] = true
	writeJSON(w, 200, out)
}

// POST /v1/reports/simple: a one-metric report from flat fields (what the assistant can express). It builds a
// definition and hands it to createReport, so every rule, role check and audit entry is the normal one.
func (s *server) createSimpleReport(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if json.Unmarshal(b, &in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	hours := 24
	if v := in["window_hours"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "window_hours must be a whole number", 400)
			return
		}
		hours = n
	}
	group := in["group_by"]
	if group == "" {
		group = "hour"
	}
	def := report.Definition{Metrics: []report.Metric{{DeviceID: in["device_id"], PointID: in["point_id"]}}, WindowHours: hours, GroupBy: group, Chart: in["chart"]}
	hl := &report.Highlight{When: strings.TrimSpace(in["highlight_when"]), WhenColor: in["highlight_color"]}
	for k, dst := range map[string]**float64{"highlight_above": &hl.Above, "highlight_below": &hl.Below} {
		if v := in[k]; v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				http.Error(w, k+" must be a number", 400)
				return
			}
			*dst = &f
		}
	}
	if hl.When != "" || hl.Above != nil || hl.Below != nil {
		def.Highlight = hl
	}
	body, _ := json.Marshal(map[string]any{"name": in["name"], "definition": def})
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(strings.NewReader(string(body)))
	s.createReport(w, r2)
}
