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
| Chart: pie, scatter, stacked, combo, secondary axis | Not built | |
| Gauge, indicator | Not built | |
| Map | Not built | Needs air-gapped tiles decision. |
| Report parameters (cascading, multivalue) | Partial | Window, group-by, layout, agg, device via URL parameters. No cascading or multivalue. |
| Expressions | Partial | KPI expression language for computed columns (arithmetic and point refs only). No functions or conditional formatting. |
| Conditional formatting, sorting, interactive sort | Not built | |
| Page layout, headers/footers, themes, logo | Partial | Header/footer text, light/dark, tenant logo. No page size/margin/orientation or page-number tokens. |
| Subreports, drill-through, bookmarks | Not built | |
| Export: PDF, XLSX, CSV, HTML | Built | |
| Export: Word, PowerPoint, XML | Not built | |
| Scheduled delivery | Built | Cron plus notification channel. |
| Versioning and restore | Built | |
| Designer: drag-drop surface, wizard, toolbox | Not built | Current UI is a form, not a design surface. |

Next units, in order: chart types and gauges in PDF/XLSX, nested groups, parameters, conditional formatting, page setup, designer surface.
