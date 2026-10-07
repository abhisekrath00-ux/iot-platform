# Report Builder parity matrix

Target (user request, 2026-10-07): enterprise grade, covering the features of Microsoft Report Builder.
Reference: Microsoft Learn pages on data regions, parameters, expressions, page layout and export
(learn.microsoft.com/en-us/sql/reporting-services/report-design/ and .../report-builder/).
Status labels: Built, Partial, Not built. "Tested" means unit tests on this machine only. Nothing here is claimed as exact parity.

| Report Builder feature | HexThings | Notes |
|---|---|---|
| Table (per-metric rows) | Built | Default layout. |
| Matrix / crosstab | Partial | Bucket rows by metric columns, agg choice, computed columns. No dynamic column groups. |
| Row groups and subtotals | Partial | Rollup by asset or site with subtotals and grand total. No arbitrary nested groups. |
| List data region | Not built | |
| Chart: line, area, bar | Built (HTML) | Inline SVG, opt-in `chart` field, unit-tested edge cases. PDF has a line chart; XLSX/CSV have none. |
| Chart: pie, scatter | Partial | Scatter of bucket averages; pie of each bucket's share of the window sum (folds to 7 + other, so it is weak for long windows). Unit-tested and rendered in headless Chrome, HTML only. |
| Chart: stacked, combo, secondary axis | Not built | |
| Gauge, indicator | Partial | Half-circle gauge of the latest bucket average across the series min..max (no custom ranges or colour bands). HTML only. |
| Map | Not built | Needs air-gapped tiles decision. |
| Report parameters (cascading, multivalue) | Partial | Window, group-by, layout, agg, device via URL parameters. No cascading or multivalue. |
| Expressions | Partial | KPI expression language for computed columns (arithmetic and point refs only). No functions or conditional formatting. |
| Conditional formatting | Partial | `highlight` {above, below} thresholds colour avg cells in per-metric HTML tables (red/amber). Not in matrix layout, PDF or XLSX; no expressions; no UI control yet. Unit-tested. |
| Sorting, interactive sort | Not built | |
| Page layout, headers/footers, themes, logo | Partial | Header/footer text, light/dark, tenant logo. No page size/margin/orientation or page-number tokens. |
| Subreports, drill-through, bookmarks | Not built | |
| Export: PDF, XLSX, CSV, HTML | Built | |
| Export: Word, PowerPoint, XML | Not built | |
| Scheduled delivery | Built | Cron plus notification channel. |
| Versioning and restore | Built | |
| Designer: drag-drop surface, wizard, toolbox | Not built | Current UI is a form, not a design surface. |

Next units, in order: chart types and gauges in PDF/XLSX, nested groups, parameters, conditional formatting, page setup, designer surface.

## Production grade and scale (user request, 2026-10-07)

Rule for every report unit: bounded work per report, no unbounded memory, and a stated scale label. Labels: Load-tested (measured here, with numbers), Bounded by design (limit enforced and unit-tested, no load run), Untested.

| Area | Label | Detail |
|---|---|---|
| HTML chart rendering | Bounded by design, microbenchmarked | Series thinned to 400 points; 10,000-bucket benchmark in chart_test.go: about 1.5 ms and 1.2 MB per chart on this 2-core machine, microbenchmark only, not a load test. |
| Report window and bucket count | Bounded by design | Window up to 90 days, fixed group-by buckets; aggregation runs in Postgres. |
| Concurrent report runs | Bounded by design, unit and DB tested | Per-process gate for preview, download and scheduled/manual run: `REPORT_MAX_CONCURRENT` (default 4), `REPORT_MAX_PER_TENANT` (default 2), `REPORT_QUEUE_WAIT_SECONDS` (default 10). Over the limit: HTTP 503 with Retry-After; a scheduled run that cannot get a slot logs and is retried on the next minute tick. Tested: cap never exceeded under 60 goroutines (race detector), per-tenant fairness, double-release safety, cancel, 503 then 200 on a real DB. Limits are per replica (N replicas = N times). Not load-tested with real report builds. |
| Report row cap | Bounded by design, unit and DB tested | `REPORT_MAX_ROWS` (default 200000 bucket rows across all metrics). Over the cap: preview and download return HTTP 413 with advice (narrower window, coarser group_by, fewer metrics); scheduled runs fail with the same message. Counted while reading, so the read stops early. Output byte size is not capped separately and PDF/XLSX memory use is not measured. |
| Horizontal scaling (several API replicas) | Untested | Report state is in Postgres; the scheduler's single-runner guarantee under multiple replicas is not verified. |
| Autoscaling | Not built | Needs deployment manifests and a measured load test on real hardware; not available on this machine. |
| High ingest throughput | See docs/scaling.md and docs/timescale-evaluation.md | Existing measured numbers there; nothing new in this unit. |

Planned scale units: scheduler leader lock verified with two processes, then a load test of report runs against a large seeded telemetry table with measured p95.
