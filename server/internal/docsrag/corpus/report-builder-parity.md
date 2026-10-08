# Report Builder parity matrix

Target (user request, 2026-10-07): enterprise grade, covering the features of Microsoft Report Builder.
Reference: Microsoft Learn pages on data regions, parameters, expressions, page layout and export
(learn.microsoft.com/en-us/sql/reporting-services/report-design/ and .../report-builder/).
Status labels: Built, Partial, Not built. "Tested" means unit tests on this machine only. Nothing here is claimed as exact parity.

| Report Builder feature | HexThings | Notes |
|---|---|---|
| Table (per-metric rows) | Built | Default layout. |
| Matrix / crosstab | Partial | Bucket rows by metric columns, agg choice, computed columns. No dynamic column groups. |
| Row groups and subtotals | Partial | Rollup by asset or site, or nested two levels (`site>asset`, `asset>site`) with an outer subtotal, inner rows ("Site / Asset") and a grand total; set in the Reports page. Two fixed levels only: no arbitrary fields, no third level, no collapse/expand, no per-group page breaks. Unit, real-Postgres (incl. cross-tenant) tested; PDF/CSV/HTML rendered and inspected, XLSX not opened in Excel. |
| List data region | Not built | |
| Chart: line, area, bar | Built (HTML) | Inline SVG, opt-in `chart` field, unit-tested edge cases. PDF has a line chart (always, first 4 metrics). XLSX: native Excel charts (line, area, bar, scatter; flat layout; max 8; one per metric with 2+ buckets; gauge/pie get none). CSV none. |
| Chart: pie, scatter | Partial | Scatter of bucket averages; pie of each bucket's share of the window sum (folds to 7 + other, so it is weak for long windows). Unit-tested and rendered in headless Chrome, HTML only. |
| Chart: stacked, combo, secondary axis | Not built | |
| Gauge, indicator | Partial | Half-circle gauge of the latest bucket average across the series min..max (no custom ranges or colour bands). HTML only. |
| Map | Not built | Needs air-gapped tiles decision. |
| Report parameters (cascading, multivalue) | Partial | Window, group-by, layout, agg, page via URL parameters. `device` is multivalue (`device=a,b,c`, up to 10). Cascading `site` -> `asset` -> `device`: an asset must lie in the chosen site and a device in both, otherwise 400 (never silently ignored); site/asset expand to their devices through the same multivalue path, so the 10-device cap applies. Reports page shows three linked pickers (asset list narrows to the chosen site, device list to both) for single-device reports. Unit, real-Postgres (incl. cross-tenant, injection-shaped input) tested; picker behaviour checked in a real browser. Only the site/asset/device chain: no user-defined parameters, no default/prompt definitions, no multivalue for other parameters, not available to customer-scoped users. |
| Expressions | Partial | KPI expression language for computed columns and KPIs: arithmetic, point refs and six pure functions (`abs round sqrt min max clamp`, fixed list, argument counts checked, nesting depth capped) and `if(a > b, then, else)` with > < >= <= == != and `and`/`or`/`not` with parentheses (and/or short-circuit; only the chosen branch runs). No text or date functions, aggregates over fields, or expressions in formatting. Unit-tested. |
| Conditional formatting | Partial | `highlight` {above, below} thresholds colour avg cells in per-metric HTML tables (red/amber). Not in matrix layout, PDF or XLSX; no expressions. Reports page has red-above/amber-below inputs (see UI row). Unit-tested. |
| Sorting, interactive sort | Not built | |
| Page layout, headers/footers, themes, logo | Partial | Header/footer text, light/dark, tenant logo, PDF page size and orientation (`page`: a4 default, a4-landscape, letter, letter-landscape; also a `page` URL parameter). Default PDF output verified byte-identical to before. No custom size or margins, no page-number tokens, no per-section breaks; HTML and XLSX ignore `page`. Reports page has a PDF page size select. Unit-tested, landscape rendered and inspected. |
| Subreports, drill-through, bookmarks | Not built | |
| Export: PDF, XLSX, CSV, HTML | Built | |
| Export: XML | Partial | `format=xml`: one series per metric with a bucket element per row (well-formed, escaped, non-finite as empty). Flat series only: no matrix or rollup. Unit and DB tested; button added to Reports.tsx but the web build was not run here. |
| Export: Word | Partial | `format=docx` (Word button on the Reports page): title, generated line, one table per metric or the matrix table, summary grouping table, header text, footer with page number, PDF page size and orientation, highlight shading. Standard-library zip, no macros or external links. No charts, logo or insights section. Unit-tested (every part well-formed XML, escaping, landscape/portrait, matrix, empty); real file converted by LibreOffice and inspected. Not opened in real Microsoft Word. |
| Export: PowerPoint | Not built | |
| Scheduled delivery | Built | Cron plus notification channel. |
| Versioning and restore | Built | |
| Designer: drag-drop surface, wizard, toolbox | Not built | Current UI is a form, not a design surface. |

Next units, in order: PowerPoint export, conditional formatting from expressions, designer surface, PDF chart types and gauges.

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

Scheduler leader lock: verified with two real OS processes on one Postgres (exactly one runs the job; SIGKILL of the leader hands over in about 50 ms, 3 of 3 runs, test `TestTwoProcessesAndKillFailover`). Not tested: a network partition or frozen leader (no TCP close; the 5 s watchdog covers connection loss, not a hung job), or processes on separate hosts.

Planned scale units: a load test of report runs against a large seeded telemetry table with measured p95.
