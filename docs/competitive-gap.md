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
| Industrial protocols | ThingsBoard IoT Gateway | Modbus RTU/TCP, OPC UA (secure modes), serial JSON | Partial (no BACnet, CAN, SNMP, LwM2M, CoAP) |
| Store and forward at the edge | Ignition | SQLite queue, delete after broker ACK | Have |
| Fleet updates with staged rollout | AWS Device Management | Staged cohorts, rollback, ack | Have (edge agent config/release); no signed firmware for end devices |
| Dashboards and widgets | ThingsBoard (300+ widgets) | 8 widget types (live value, gauge, trend, bars, status, table, 24h stats, indicator lamp) with thresholds, drag-drop layout and sizing, wall mode with rotation, product tour, light/dark glass UI | Partial (8 widgets, still far from 300+; pure logic unit-tested, new widgets type-checked and built but not browser-checked; drag/wall covered by a manual browser e2e, not CI) |
| Rule engine / workflows | ThingsBoard, Losant | Threshold rules, flows (trigger, condition, delay, notify) with versions and simulator | Partial (no scripting node, no device-state write node; cooldown and latch exist) |
| Alarm lifecycle (ack, clear, assign) | ThingsBoard | Open, acknowledged, resolved with notes and history in API and UI | Have (assignment to an admin or operator with an assignee filter, tested; escalation chains exist separately, see Notifications) |
| Edge-side auto-discovery | Ignition, ThingsBoard gateway, Siemens Industrial Edge | Gateway scans serial (baud/parity/address sweep), local subnets and BACnet on its own, matches built-in and cached profiles, works offline, proposals on the local page and dashboard; optional edge auto-add for confident matches | Partial (tested with fakes and a simulator; no real RS-485 hardware; no SNMP or mDNS pass yet) |
| Notifications | ThingsBoard | SMTP, Slack, webhook, Kafka, AMQP, scheduled reports; escalation chains (up to 5 steps per severity, delay-based, stops on acknowledge, `docs/escalation.md`) | Partial (no SMS or Teams; no on-call schedules; optional capped repeat-until-ack reminders and quiet hours (non-critical reminders only) exist) |
| Reports, scheduled delivery | Ignition, ThingsBoard | Builder with preview, 15min-week buckets, CSV/HTML, cron delivery | Have |
| Digital twin: assets, hierarchy, relations | ThingsBoard, Azure | Site, gateway, device plus a per-device twin card with health score (2.5D SVG, not 3D) | Partial (no asset hierarchy or relations) |
| Device attributes, tags, groups, fleet search | AWS, ThingsBoard | Tags, fleet search and filters | Partial (tags only; no groups or typed attributes) |
| Desired/reported state (shadow, twin) | AWS, Azure | Reported state and attributes (`docs/device-shadow.md`, tested) | Partial (no desired state: that would actuate and stays under control gating) |
| Data retention and downsampling | Ignition (QuestDB historian), ThingsBoard | Hourly and stored daily rollups, per-tenant raw and hourly retention API, reports read rollups for purged ranges; device series API takes `hours` up to 1 year (raw to 48h, hourly buckets to 14d, daily beyond, from raw plus rollups) with a range picker on the device page | Partial (no compression; no UI page for the policy yet) |
| API keys / service tokens | all | Hashed, expiring, revocable keys; viewer/operator role; endpoint-group scopes; per-key rate limit; never able to approve commands; audited | Have (coarse scopes, per-replica limit) |
| Secrets management | Ignition 8.3 | Per-tenant encrypted store: AES-256-GCM, master key from `SECRETS_KEY` (never in the DB), tenant+name bound so copied ciphertext fails, write-only admin API, audited without values. No connector consumes it yet (the HTTP request node is the planned first user); no key rotation tooling, no external KMS/Vault | Partial |
| Edge rules, edge compute | Losant, ThingsBoard Edge | Allowlisted commands, edge gate (expiry, replay, TTL), ack path, simulated actuator only | Partial (no real actuator drivers, by design until hazard analysis) |
| Mobile app | ThingsBoard | Responsive web only | Missing |
| Bulk device provisioning | AWS | CSV import `POST /v1/devices/bulk` (gateway_id, profile_id, name, tags, asset_id; all-or-nothing, dry run, 500 rows, line-numbered errors). Devices page file picker with dry-run preview (API integration-tested, UI not browser-tested). | Built |
| AI access | none of the above natively | MCP server, 12 tools (read tools, flow drafts, forecast, related signals; none actuate) | Have |
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
| Asset hierarchy, aspects, asset files | Insights Hub Asset Manager, ThingsBoard | Partial (this pass, tested): tenant-scoped asset tree (max depth 8, delete only when empty), devices attach to an asset, Assets page (migration 0018). No typed aspects, no asset files, no relations beyond parent/child, dashboards and rules do not yet filter by asset | Next: filter fleet and rules by asset |
| KPI / derived points (formulas) | Insights Hub Monitor | Partial (this pass, tested): named KPIs from a safe arithmetic expression over `{device.point}` latest values (no functions, no code execution, depth and size capped), live values with a stale-input flag, KPIs page (migration 0019). Computed on read; history, charting, thresholds and alerting on KPIs are not built | |
| Flow canvas with function, switch, change, debug nodes | Node-RED, Visual Flow Creator | Partial: trigger, condition, delay, notify nodes, versions, simulator; no JS function node, no switch/change/debug, no free-form canvas | Build: node types, import/export first |
| Flow import and export (JSON) | Node-RED | Have (this pass, integration-tested): export of the published version as `hexmon-flow/1`, import as an unpublished draft, channel ids replaced by type:target labels. Not Node-RED file compatible. | |
| Custom node SDK, sandboxed function node | Node-RED | Missing | Needs a sandbox decision (WASM or goja) and security review |
| Outbound webhooks and HTTP request node | ThingsBoard, Node-RED | Partial (this pass, tested): `webhook` notification channel used by alert rules, flows and scheduled reports. Admin-configured URL only (flows cannot supply URLs), no redirects, loopback/link-local/metadata addresses blocked at dial time, optional HMAC signature (`WEBHOOK_SIGNING_SECRET`). No arbitrary HTTP request node, no response handling in flows | |
| Geofencing and maps | ThingsBoard | Missing | Build only with a self-hosted tile source |
| OTA firmware for end devices | ThingsBoard, Insights Hub | Partial: edge agent releases and staged rollout only | Not planned for MCU firmware without signing design |
| White-label (logo, colours, title) | ThingsBoard PE | Partial (this pass, tested): per-tenant product name and accent colour (contrast-checked), applied to sidebar, tab title and buttons; migration 0020. No logo upload, no custom domain, no email template branding | |
| SSO | all | Have (OIDC) | SAML not built |
| Mobile app / PWA | ThingsBoard | Partial: responsive web plus a web manifest, icon and theme colour so it can be added to a home screen. No service worker, so no offline mode and no push notifications; native app not planned | |
| Edge compute | ThingsBoard Edge, Insights Hub edge analytics | Partial | See table above |
| Protocols: BACnet, CoAP, LoRaWAN, SNMP, MQTT direct, LwM2M | ThingsBoard gateway | Missing (HTTP ingest and Modbus/OPC UA/serial exist) | Adapter-only claims are not made; each needs a device or simulator to test |
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
| Node-RED interchange | Export of a flow as a Node-RED flow array, and import of a supported subset (switch, change, delay, debug, function plus our own trigger/notify nodes). Anything else (inject, mqtt, http, link nodes, JSONata, etc.) is refused and listed; nothing is approximated. Exported files do not run in Node-RED because trigger and notify are Hexmon node types. |
| Running the real Node-RED inside the platform | **Not built.** Decision and reasons in [node-red-decision.md](node-red-decision.md). The Node-RED palette (thousands of community nodes) is therefore not available. |
| MCP text-to-flow | Built: `describe_flow_nodes`, `validate_flow_graph`, `draft_flow_graph`. The calling LLM writes the graph; the server validates it. Drafts only: never published or enabled, operator/admin token required, no function nodes. Tested. |
| Natural-language text to graph inside the UI | **Not built.** An air-gapped install has no language model to call. Use any MCP-capable assistant against `/mcp`. |
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
| Expressions (computed columns, formatting, conditionals) | Up to 5 computed columns on the matrix layout, from the safe KPI expression language (numbers, + - * /, point references of the report; validated; no functions or loops). HTML, PDF, Excel, CSV | Partial (no number formatting or conditionals; matrix layout only) | 7 |
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
