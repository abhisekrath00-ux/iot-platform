# Competitive gap analysis

Compared against public documentation of ThingsBoard, AWS IoT, Losant and
Ignition (sources at the end). "Have" means implemented and tested in this
repo; "Partial" means a real but narrower version; "Missing" means not built.
This is a product-judgment document, not a marketing claim: rows are marked
from the code, not from intent.

| Capability | Reference platform | This product | Status |
|---|---|---|---|
| Multi-tenancy, tenant-scoped data | ThingsBoard | Tenant id on every query, integration-tested isolation | Have |
| RBAC, SSO, audit log | ThingsBoard, Ignition | Roles, OIDC, audit table and page | Have |
| Device authentication by X.509 | ThingsBoard, AWS | mTLS per gateway, broker ACLs from cert CN | Have (gateways); direct MCU devices only designed |
| Device profiles | ThingsBoard | Profiles with per-protocol point maps, drive generated edge config | Have |
| Industrial protocols | ThingsBoard IoT Gateway | Modbus RTU/TCP, OPC UA (secure modes), serial JSON | Partial: Modbus, OPC UA, serial, SNMP, BACnet, IEC 104, DNP3, CoAP, IEC 61850 and LoRaWAN ingest are built, simulator-tested only (see connectors.md). Missing: LwM2M, CAN; no writes on the read-only protocols |
| Store and forward at the edge | Ignition | SQLite queue, delete after broker ACK | Have |
| Fleet updates with staged rollout | AWS Device Management | Staged cohorts, rollback, ack | Have (edge agent config/release); no signed firmware for end devices |
| Dashboards and widgets | ThingsBoard (300+ widgets) | 8 widget types (live value, gauge, trend, bars, status, table, 24h stats, indicator lamp) with thresholds, drag-drop layout and sizing, wall mode with rotation, product tour, light/dark glass UI | Partial (8 widgets, still far from 300+; pure logic unit-tested, new widgets type-checked and built but not browser-checked; drag/wall covered by a manual browser e2e, not CI) |
| Rule engine / workflows | ThingsBoard, Losant | Threshold rules, flows (trigger, condition, delay, notify) with versions and simulator | Partial (no scripting node, no device-state write node; cooldown and latch exist) |
| Alarm lifecycle (ack, clear, assign) | ThingsBoard | Open, acknowledged, resolved with notes and history in API and UI | Have (assignment to an admin or operator with an assignee filter, tested; escalation chains exist separately, see Notifications) |
| Edge-side auto-discovery | Ignition, ThingsBoard gateway, Siemens Industrial Edge | Gateway scans serial (baud/parity/address sweep), local subnets and BACnet on its own, matches built-in and cached profiles, works offline, proposals on the local page and dashboard; optional edge auto-add for confident matches | Partial (tested with fakes and a simulator; no real RS-485 hardware; no SNMP or mDNS pass yet) |
| Notifications | ThingsBoard | SMTP, Slack, webhook, Kafka, AMQP, scheduled reports; escalation chains (up to 5 steps per severity, delay-based, stops on acknowledge, `docs/escalation.md`) | Partial (no SMS or Teams; on-call rotations of channels (tested; no per-person calendars, overrides or swaps); optional capped repeat-until-ack reminders and quiet hours (non-critical reminders only) exist) |
| Reports, scheduled delivery | Ignition, ThingsBoard | Builder with preview, 15min-week buckets, CSV/HTML, cron delivery | Have |
| Digital twin: assets, hierarchy, relations | ThingsBoard, Azure | Site, gateway, device plus a per-device twin card with health score (2.5D SVG, not 3D) | Partial (asset tree plus typed asset relations with a downstream walk; no 3D, no relation-driven dashboards) |
| Device attributes, tags, groups, fleet search | AWS, ThingsBoard | Tags, fleet search and filters | Partial (tags only; no groups or typed attributes) |
| Desired/reported state (shadow, twin) | AWS, Azure | Reported state and attributes (`docs/device-shadow.md`, tested) | Partial (no desired state: that would actuate and stays under control gating) |
| Data retention and downsampling | Ignition (QuestDB historian), ThingsBoard | Hourly and stored daily rollups, per-tenant raw and hourly retention API, reports read rollups for purged ranges; device series API takes `hours` up to 1 year (raw to 48h, hourly buckets to 14d, daily beyond, from raw plus rollups) with a range picker on the device page | Partial (no compression; no UI page for the policy yet) |
| API keys / service tokens | all | Hashed, expiring, revocable keys; viewer/operator role; endpoint-group scopes; per-key rate limit; never able to approve commands; audited | Have (coarse scopes, per-replica limit) |
| Secrets management | Ignition 8.3 | Per-tenant encrypted store: AES-256-GCM, master key from `SECRETS_KEY` (never in the DB), tenant+name bound so copied ciphertext fails, write-only admin API, audited without values. No connector consumes it yet (the HTTP request node is the planned first user); no key rotation tooling, no external KMS/Vault | Partial |
| Edge rules, edge compute | Losant, ThingsBoard Edge | Allowlisted commands, edge gate (expiry, replay, TTL), ack path, simulated actuator only | Partial (no real actuator drivers, by design until hazard analysis) |
| Mobile app | ThingsBoard | Responsive web only | Missing |
| Bulk device provisioning | AWS | CSV import `POST /v1/devices/bulk` (gateway_id, profile_id, name, tags, asset_id; all-or-nothing, dry run, 500 rows, line-numbered errors). Devices page file picker with dry-run preview (API integration-tested, UI not browser-tested). | Built |
| AI access | none of the above natively | MCP server, 14 tools (read tools, flow drafts, forecast, related signals, alert root-cause hints, level-shift detection; none actuate) | Have |
| Air-gapped deploy | ThingsBoard (self-host) | Offline bundle, self-hosted assets, verified install scripts | Have (dry-run on a clean VM still pending) |
| Read caching, HA | ThingsBoard microservices | Per-replica response cache, leader election for scheduler | Partial |

## Parity audit, 2026-10-01 evening (ThingsBoard, Siemens Insights Hub, Node-RED, Ignition)

Status words: Have (built and tested), Partial, Missing. "Not planned" needs an owner decision or hardware.
Siemens items are from its public documentation (sources below), not hands-on use.

| Capability | Compared with | Status here | Plan |
|---|---|---|---|
| Generic HTTP device ingest | ThingsBoard HTTP API, Insights Hub | Have (this pass, integration-tested) | |
| Automatic monthly telemetry partitions | ThingsBoard, Ignition historian | Have (this pass, tested) | |
| Rules by asset or device type, activated for many instances | Insights Hub Monitor | Have (this pass, tested): a rule may carry `profile`; it then covers every device of that profile, one open alert per rule and device (migration 0016). Rules form has a profile field | | |
| Anomaly detection on a time series | Insights Hub Predict | Partial (this pass, tested): robust median/MAD outlier detection per point via `GET /v1/telemetry/anomalies` and a card on the device page. Explains outliers; no forecasting, no learned model. Automatic alerting is a separate, simpler rule: a reading N standard deviations (mean/std over a window you set, at least 30 samples, flat baselines never fire) from its own history, plus KPI-outside-a-band rules checked once a minute by one replica (stale KPIs never alert). Both are created by a user, tested with integration tests, and share the normal alert dedupe and notification path. The device-page outlier card uses the median/MAD method and the alert rule uses mean/std, so they can disagree | Gap: no forecasting or learned model; alert rule does not use the median/MAD detector |
| Asset hierarchy, aspects, asset files | Insights Hub Asset Manager, ThingsBoard | Partial (this pass, tested): tenant-scoped asset tree (max depth 8, delete only when empty), devices attach to an asset, Assets page (migration 0018). Typed directed relations between assets (migration 0040, API and Assets page card, tested): feeds, powers, backs_up or any lower-case name, with a downstream "what does this reach" walk (cycle-safe, depth 10). The UI links assets only; the API also accepts devices. No typed aspects, no asset files, no relation-based dashboards or rules, dashboards and rules do not yet filter by asset | Next: filter fleet and rules by asset |
| KPI / derived points (formulas) | Insights Hub Monitor | Partial (this pass, tested): named KPIs from a safe arithmetic expression over `{device.point}` latest values (no functions, no code execution, depth and size capped), live values with a stale-input flag, KPIs page (migration 0019). Computed on read; history, charting, thresholds and alerting on KPIs are not built | |
| Flow canvas with function, switch, change, debug nodes | Node-RED, Visual Flow Creator | Have (see the node-graph section below): node-graph editor with function (sandboxed JS), switch, change, debug, inject, HTTP request, context nodes, versions and a simulator. Not the Node-RED palette, no custom node SDK | Superseded by the node-graph rows |
| Flow import and export (JSON) | Node-RED | Have (this pass, integration-tested): export of the published version as `hexmon-flow/1`, import as an unpublished draft, channel ids replaced by type:target labels. Not Node-RED file compatible. | |
| Custom node SDK, sandboxed function node | Node-RED | Partial: admin-defined custom node types (saved sandboxed function presets, see function-nodes.md); no SDK, no typed parameters | Needs a sandbox decision (WASM or goja) and security review |
| Outbound webhooks and HTTP request node | ThingsBoard, Node-RED | Partial (this pass, tested): `webhook` notification channel used by alert rules, flows and scheduled reports. Admin-configured URL only (flows cannot supply URLs), no redirects, loopback/link-local/metadata addresses blocked at dial time, optional HMAC signature (`WEBHOOK_SIGNING_SECRET`). No arbitrary HTTP request node, no response handling in flows | |
| Geofencing and maps | ThingsBoard | Partial: hand-entered device positions, circular zones, plain canvas map, optional self-hosted tile layer (see the 2026-10-03 pass). No enter/exit alerts, no polygons, no live GPS tracks | Built with the limits listed |
| OTA firmware for end devices | ThingsBoard, Insights Hub | Partial: edge agent releases and staged rollout, now with Ed25519-signed manifests verified on the edge (unit-tested only, see fleet.md) | MCU/end-device firmware flashing not built (needs real devices and vendor bootloaders) |
| White-label (logo, colours, title) | ThingsBoard PE | Partial (this pass, tested): per-tenant product name and accent colour (contrast-checked), applied to sidebar, tab title and buttons; migration 0020. No logo upload, no custom domain, no email template branding | |
| SSO | all | Have (OIDC) | SAML via a self-hosted OIDC bridge, see sso-saml.md (native SAML deliberately not built, bridge setup not tested here) |
| Mobile app / PWA | ThingsBoard | Partial: responsive web plus a web manifest, icon and theme colour so it can be added to a home screen. Offline app shell via a self-hosted service worker (static UI only, never API data); no push notifications and no offline data; native app not buildable here | |
| Edge compute | ThingsBoard Edge, Insights Hub edge analytics | Partial | See table above |
| Protocols: BACnet, CoAP, LoRaWAN, SNMP, MQTT direct, LwM2M | ThingsBoard gateway | Partial (BACnet, SNMP, CoAP, LoRaWAN, IEC 104, DNP3, IEC 61850, LwM2M object reads and SocketCAN receive built; all simulator-tested only, no real devices; LwM2M is not a server) | Adapter-only claims are not made; each needs a device or simulator to test |
| Maintenance windows (planned work) | Insights Hub, Ignition, ThingsBoard | Partial (tested locally): device or asset window up to 7 days, warning/info alerts recorded but not notified, critical never held, release notice if still open. Rule alerts only; not flows or edge rules. | |
| Control target registry (allowlist for flow control nodes) | Ignition, Node-RED | Partial (tested locally): admin-defined targets, bounded values, per-target on/off, approval or automatic (automatic only for alarm outputs). The `control` flow node raises approval requests against it (tested locally). Automatic path for alarm-output targets set to automatic (tested locally with fakes; edge needs `allow_automatic_commands` and a configured alarm output; Modbus writes never automatic). | Not tested on a real broker, gateway or siren |
| Historian | Ignition | Partial: raw plus hourly and daily rollups, partitions | Retention policy needs an owner decision |

Sources: Insights Hub applications overview https://documentation.mindsphere.io/MindSphere/apps-and-solutions/overview.html ;
capability packages https://assets.ctfassets.net/17si5cpawjzf/5cWrwqUy9OFRCeiyJ5B9wa/3885a769ba6031ba7e5bd3283452e1fd/Insights_Hub_CapabilityPackages_ProductSheet_v2.4.pdf ;
Monitor rules by asset type and Predict anomaly detection https://blogs.sw.siemens.com/insights-hub/2023/04/17/insights-hub-and-the-industrial-iot-whats-new-april-2022/ ;
Visual Flow Creator API https://developer.siemens.com/insights-hub/docs/apis/advanced-visual-flow-creator/api-visual-flow-creator-overview.html

## Ranked build list

Status as of 2026-10-01 morning: items 1 to 7 below are built (see the table for caveats). Remaining gaps are the table rows still marked Partial or Missing.

Chosen for customer impact on the stated use (energy meters, door sensors,
STM32 and PLC sources, enterprise monitoring) and for low risk:

1. Alarm lifecycle: acknowledge, clear, comment, history. Without it the
   alert page is a log, not an operations tool.
2. Device attributes and tags plus fleet search and filters. Everything else
   (groups, dashboards per group, bulk actions) depends on it.
3. Data retention policy with downsampled rollups, so long-running sites do
   not degrade.
4. API keys for integrations (hashed, scoped, revocable, audited). Needed for
   SCADA and BI pulls without sharing user logins.
5. Digital twin page per device (health, freshness, quality, attributes).
   Assets and relations come after attributes.
6. Wall/TV mode with drag-and-drop layout and a larger widget library.
7. Latch/dedupe node in flows to stop repeat alerts.

Not planned without owner input: SMS/Teams channels (external accounts),
mobile app, BACnet/LwM2M/CoAP (need hardware or a customer requirement),
firmware signing for end devices.

## Sources (fetched 2026-10-01)

- ThingsBoard, Why ThingsBoard: https://thingsboard.io/docs/pe/why-thingsboard/
- AWS IoT Device Management features: https://aws.amazon.com/iot-device-management/features/
- Losant workflows: https://docs.losant.com/workflows/overview/
- Ignition introduction (8.3): https://www.docs.inductiveautomation.com/docs/8.3/getting-started/introducing-ignition

## Node-graph flows, function nodes, Node-RED and MCP (this pass)

| Item | Status |
|---|---|
| Node-graph flow executor (trigger, switch, change, condition, delay, debug, notify, function) | Built and tested (unit tests, plus API tests that run a graph end to end). Legacy linear flows still run unchanged; a test proves a legacy flow and its converted graph give identical actions. Fan-out is capped at 500 node visits per run. |
| Debug node | Output is stored in the flow run log (`flow_runs.detail`). No live debug sidebar yet. |
| Function node (sandboxed JS) | Built, tested, off by default, admin-only. See [function-nodes.md](function-nodes.md) for limits and weaknesses. No outside security review. |
| Node-RED interchange | Export of a flow as a Node-RED flow array, and import of a supported subset (switch, change, delay, debug, function plus our own trigger/notify nodes). Anything else (inject, mqtt, http, link nodes, JSONata, etc.) is refused and listed; nothing is approximated. Exported files do not run in Node-RED because trigger and notify are HexThings node types. |
| Running the real Node-RED inside the platform | **Not built.** Decision and reasons in [node-red-decision.md](node-red-decision.md). The Node-RED palette (thousands of community nodes) is therefore not available. |
| MCP text-to-flow | Built: `describe_flow_nodes`, `validate_flow_graph`, `draft_flow_graph`. The calling LLM writes the graph; the server validates it. Drafts only: never published or enabled, operator/admin token required, no function nodes. Tested. |
| Natural-language text to graph inside the UI | **Not built.** An air-gapped install has no language model to call. Use any MCP-capable assistant against `/mcp`. A small rule-based question box exists on the Fleet page (`POST /v1/ask`, tested): open or critical alerts, alerts on a device, latest value of a point, devices with no reading in 15 minutes, devices by tag. It is phrase matching over fixed read-only queries, shows how it read the question, refuses everything else, and cannot write or actuate. It is not a language model and does not take free-form questions. |
| Visual graph editor | See the Flows page status in the changelog below. |

## Microsoft Report Builder parity (added 2026-10-02)

Reference: Microsoft Report Builder / SSRS paginated reports. Sources:
https://learn.microsoft.com/en-us/sql/reporting-services/report-design/tables-matrices-and-lists-report-builder-and-ssrs?view=sql-server-ver17 ,
https://learn.microsoft.com/en-us/sql/reporting-services/report-design/report-parameters-report-builder-and-report-designer?view=sql-server-ver17 ,
https://learn.microsoft.com/en-us/sql/reporting-services/report-builder/export-reports-report-builder-and-ssrs?view=sql-server-ver17
(export targets listed there: PDF, Accessible PDF, Word, Excel, PowerPoint, image, CSV, XML/Atom).

Status is from this repo's code. Nothing here is "Have" unless built and tested.

| Report Builder capability | This product | Status | Value rank |
|---|---|---|---|
| Report parameters (date range, asset, device, threshold) | Run-time overrides of window, grouping, layout, aggregate and device (`device=` runs a one-device report for another device; refused for multi-device reports and computed columns) on every download, validated, stored definition untouched; tested | Partial (no asset parameter, no cascading or multi-value) | 1 |
| Export to PDF | Paginated PDF (built-in font, page numbers, repeating headers) with a vector line chart per metric (avg line, min and max lines, value range, first and last bucket time; first 4 metrics in matrix layout), unit tested and inspected as a rendered page | Have (basic: line charts only, ASCII text only, no bar or stacked charts, charts are PDF only: HTML and Excel have none) | 2 |
| Export to Excel (.xlsx) | Single-sheet workbook, stdlib writer, validated as a zip with escaped cells | Have (basic: no styles, formulas or multiple sheets; not opened in real Excel) | 3 |
| Tables with row groups and subtotals | Optional summary by asset or site: per (group, point) rows with devices/samples/avg/min/max/sum and per-point grand totals; HTML, CSV, XLSX, PDF. Points are never added together | Have (tested; one grouping level, not nested; not opened in real Excel) | 4 |
| Matrix (cross-tab, e.g. device by day) | Matrix layout: time rows by point columns, avg/min/max/sum, column totals; HTML, PDF, Excel, CSV | Have (basic: one measure, one level of grouping) | 5 |
| Charts embedded in a report | Preview chart in builder UI | Partial (not rendered into delivered output) | 6 |
| Expressions (computed columns, formatting, conditionals) | Up to 5 computed columns on the matrix layout, from the safe KPI expression language (numbers, + - * /, six numeric functions (abs round sqrt min max clamp), if(a > b, x, y), point references of the report; validated; no functions or loops). HTML, PDF, Excel, CSV | Partial (no number formatting or conditionals; matrix layout only) | 7 |
| Page header/footer, page numbers, hard page breaks | PDF has "Page n of m" on every page, optional header and footer text, a repeating column header, and a chart never splits across pages | Partial (custom header and footer text, 80 characters each, on every PDF page and in the HTML, tested and checked on a rendered page; no logo, no manual page breaks; HTML does not repeat them per printed page) | 8 |
| Scheduled delivery to email/Slack | Cron delivery wired | Have | - |
| Saved, versioned report definitions | Edit a saved report (`PUT /v1/reports/{id}`), automatic version history (each edit or restore saves the previous state first; newest 100 kept), restore any version as a new one (`GET .../versions`, `POST .../versions/{v}/restore`); Edit and History on the Reports page; admin and operator can change, tenant-isolated, audited; tested | Have (no diff view, no named or pinned versions; scheduled runs always use the current version) | 9 |
| Drill-through and subreports | None | Missing | 10 |
| Word, PowerPoint, image export | None | Missing, not planned (PDF and Excel cover the need) | - |

Build order: parameters, PDF, Excel, grouped tables with subtotals, matrix,
embedded charts, expressions (computed columns are built; see the table). PDF must work air-gapped, so it is generated
server-side with a Go library or a pure-Go renderer; no browser or cloud
service. Expressions will be a small safe expression language, not the
function node sandbox.

| AI assistant (agentic, any OpenAI-compatible model, confirm gate) | Built, fake-model-tested only. Slack and email access built, simulator-tested only. See [assistant.md](assistant.md). |

## Gap-list pass, 2026-10-03
| Item | Status |
|---|---|
| Dashboard import/export | Built: `GET /v1/dashboards/{id}/export`, `POST /v1/dashboards/import` (`hexmon-dashboard/1`), UI buttons. Widgets on devices the target tenant lacks are kept and listed. Tested. |
| Flow split and join nodes | Built and unit-tested. Split takes a list variable (the HTTP node can now pick a JSON list of plain values, max 100); join combines everything that reaches it in one run (list, sum, avg, min, max, count). Not exported to Node-RED files. Join does not wait across separate runs. |
| Flow inject (timer) and HTTP request nodes | Already built earlier; the previous version of this list wrongly showed them missing. |
| Device groups | Built (migration 0043): named sets of devices, API `/v1/groups`, fleet filter `?group_id=`, Devices page chips and editor. Tested. No nested groups, no group-wide rules or dashboards yet. |
| Typed device attributes | Built (migration 0043): admin-defined attribute types (text, number, yes/no, one-of list, required flag) enforced when device attributes are saved; undefined names stay free-form. Tested. No units conversion, no shared/client scopes. |
| 2FA / MFA | Sign-in is by the customer's SSO provider, which owns MFA; the platform keeps no passwords unless an operator opts in to local sign-in (LOCAL_LOGIN=1, see saas-design.md). Built on top: an optional TOTP (RFC 6238, tested against the RFC vectors) second factor for approving control commands, switched on per workspace (`require_totp_approval`); secret sealed with `SECRETS_KEY`, one use per code, removal needs a current code, refusals audited. Not built: recovery codes, WebAuthn, TOTP at sign-in. |

### 2026-10-03 later: KPI history and widgets
- KPI history: `GET /v1/kpis/{id}/history?hours=` (1-720), hourly averages of each input from rollups plus raw, then the expression. Computed on read, nothing stored. Trend chart with 24 h / 7 d / 30 d on the KPIs page. Tested against a real DB.
- Widgets are now 10 types: added an open-alerts list and a plain-text note (never rendered as HTML). UI only, covered by type checks; no new server logic.
- Asset files: upload/list/download/delete per asset (`cmd/api/assetfiles.go`, migration 0045). Stored in Postgres, 5 MB and 50 files per asset, extension allowlist (pdf, png, jpg, txt, csv) with a content check, always served as an attachment with nosniff and a sandbox CSP. Operators and admins write, anyone reads, audited. Tested against a real DB. No virus scanning (none available air-gapped); the file is never rendered by the app.
- Flow context: a `context` node (get / set / add-to) with flow or global scope, numeric values only, 100 keys per scope, kept in Postgres (`flow_context`, migration 0046, `internal/flow/context.go`). Add-to is atomic (tested with 20 parallel increments). Dry runs do not read or write. The key cap is checked before the write, so two simultaneous first-writes could pass the cap by a key or two. Context holds numbers only, not objects or strings, and function (JavaScript) nodes cannot read it. Subflows are not built yet.
- Map and geofences: devices get a hand-entered lat/lon, tenants get named circular zones (`cmd/api/geomap.go`, migration 0047), and the Map page plots both as plain SVG with no tile server (air-gapped). Zone membership is computed on read (haversine, tested). Not built: a street-map tile layer (needs a self-hosted tile source), live position from telemetry, polygons, enter/exit alerts.
- Flow fragments (partial subflows): save a flow minus its start node as a named fragment (one entry node, no cycles, validated server side, `internal/flow/fragment.go`, migration 0048), then insert copies into any flow with unique ids. Function, HTTP and control nodes in a fragment need an admin to save. Fragments are copied, not live references: editing a fragment does not update flows that already use it, and a fragment is not a single node on the canvas. Live by-reference subflows are a separate feature, see subflows.md.
- Report insights and period comparison (`internal/report/insights.go`, `cmd/api/reportcompare.go`): optional section in HTML and PDF with, per metric, average, trend (straight-line fit across the window, flat under 5%), peak, unusual buckets (median/MAD robust z above 3.5, needs at least 3 buckets) and, with Compare, the change against the window just before (windows up to 45 days). Labelled statistical, descriptive only: no causes, no forecasts. Not in CSV or XLSX. Not built: logo images, custom page breaks, light/dark PDF themes.
- Customer/sub-customer hierarchy and per-customer user scoping: built as a read-only device/alert/telemetry scope with default-deny enforcement and leak tests (see customers-design.md). Not built: scoped dashboards, reports, flows, per-customer branding. Next: multi-user SaaS layer (user management, per-tenant RBAC, signup) on top of it.
- OEE template (KPIs page): builds availability x performance x quality as a normal KPI expression from four points and an ideal cycle time (`web/src/lib/oee.ts`, expression verified against the real KPI parser in `internal/kpi/oee_test.go`). It uses the latest reading of each point, so it suits counters and timers that accumulate over a shift; it is not a time-windowed OEE and has no downtime reason codes.
- White-label logo: admins upload a PNG or JPEG (100 KB, 16-1024 px, decoded and checked, never SVG) shown in the sidebar beside the existing product name and accent (`cmd/api/brandinglogo.go`, migration 0049). Not built: custom domain, logo in PDF reports and e-mails.
- LwM2M and CAN drivers added, both read-only and simulator-tested only (see docs/connectors.md for exactly what is and is not covered). LwM2M is CoAP object reads, not a LwM2M server. CAN is SocketCAN receive with per-point signal definitions, no DBC, no CAN FD or J1939. OTA firmware is still not built (needs real devices to be meaningful).
- Historical data import: `POST /v1/telemetry/import?device_id=&dry_run=1` (admin, CSV `ts,point,value[,unit]`, 20,000 rows / 4 MB, up to 5 years back). All-or-nothing with line-numbered problems, same range checks as live ingest, idempotent re-upload, hourly rollups recomputed for touched hours (kept where a rollup already summarises more samples than raw data holds). Imported history never runs alert rules or flows. Devices page has a file picker with dry-run preview (API integration-tested; UI type-checked only, not browser-tested). Rules by asset (device profile) type already existed, see the earlier row.
- Multi-signal explorer (page `/explorer`): overlay up to six points from any devices over 24 h to 90 d (hourly averages to 14 d, daily beyond, from the rollups), scale each to 0..1, z-score or raw, plus a Pearson correlation table over shared timestamps (refuses under 5 shared samples or constant series). Statistical, computed in the browser, labelled "correlated with, not caused by". Helpers unit-tested (`web/src/lib/explorer.ts`); the page itself was only checked in a headless browser against demo data, no automated UI test.
- Sigfox and Actility ThingPark inbound: the existing LoRaWAN webhook now also accepts a ThingPark `DevEUI_uplink` and a Sigfox callback body (numeric strings parsed, raw hex ignored). Tested with hand-written samples only; no real network server touched; no downlink, no vendor API polling. See docs/connectors.md.
- Learned forecast: ridge autoregression selectable next to Holt-Winters in the Forecast card, labelled `learned`, same backtest gate. Fitted per request on the series itself, no pretrained model, no cross-device learning, no deep learning. Honest label in docs/ai-design.md. A trained, persisted or multivariate ML model (Insights Hub Predict style) is NOT built.
- Tenant logo in report PDFs: the uploaded PNG/JPEG is drawn top right on page 1 (aspect kept, at most 90x28 pt, sampled down to 240 px, transparency composited over white, embedded Flate RGB, no new dependency). Unit-tested for embedding and size; visually checked on one rendered page in poppler only, not in Acrobat or other viewers. Pagination with repeating headers and "Page n of m" already existed. Still not built: light/dark PDF themes, logo in HTML, XLSX or CSV exports.
- Asset aspects (typed asset attributes): `assets.attributes` (migration 0050), `PUT /v1/assets/{id}/attributes`, attribute definitions now carry `applies_to` (device, asset or both; existing definitions stay device-only). Same type, enum and required checks as device attributes, scoped by `applies_to`. Assets page has an editor; Settings definitions form has the selector. API integration-tested; UI type-checked only. Not built: attribute inheritance down the asset tree, or asset-type templates that bundle several aspects.
- Map tiles: optional, deployer-configured self-hosted tile server (`MAP_TILE_URL`), relayed through the API so CSP and air-gap stay intact; Web Mercator layer under the device and zone overlay. Without the variable the plain canvas is unchanged. Tested with a fake tile server and screenshot; no real OSM/TileServer GL tested; nothing bundled.
- Explorer analysis: per-signal statistics table (samples, min, avg, max, first-to-last change, least-squares trend per day) and a "compare with previous period" overlay with average change. Descriptive only, from hourly/daily averages in the browser; no seasonality adjustment, so a period comparison across a weekend and a weekday is not like for like. Unit-tested helpers; page checked in a headless browser only.

- Live by-reference subflows: built, see subflows.md (version-pinned, expansion at save time, manual refresh and publish). Not built: nesting, parameters, named exits.

- Multi-user workspace layer (user management, optional local sign-in, tenant provisioning tool, sign-in page): first slice built, see saas-design.md for what is and is not covered (no custom roles, invitations, MFA at sign-in, quotas or billing).

## Optional OpenSearch / Elasticsearch telemetry sink

| Capability | Competitor | Status | Note |
|---|---|---|---|
| Push telemetry into a customer's own OpenSearch/Elasticsearch | ThingsBoard (rule-engine external nodes), Insights Hub integrations | Partial | One-way, idempotent, off by default, LAN-only if you wish. Tested against a fake `_bulk` endpoint only. See search-sink.md. No index templates, no read-back, no retention sync. |
