# Feature status: what was asked, what exists

Audit of 2026-10-04, written from the code and docs in this repo, not from memory. One label per row.

- **Built**: implemented, with automated tests that run against a real Postgres or in-process fakes.
- **Partial**: a real but narrower version; the limit is stated.
- **Simulator-only**: code exists and passes tests against a simulator or fake that I wrote from the spec. Never run against a real device, broker, model, Slack workspace or mail gateway.
- **Not built**: does not exist.

Across everything: about 444 Go test functions (server 322, edge 122) and 55 web tests pass locally (full gate plus an empty-database run before each push). **Remote CI has not run since the GitHub Actions minutes ran out, so nothing here is remote-verified.** No outside security review has been done. UI pages were checked in headless Chrome by hand on a subset, not by an automated browser suite.

## Product basics (the original brief)
| Asked for | Status | Notes |
|---|---|---|
| Edge agent on an SBC reading UART/USB serial sensors, forwarding to a server | Built / simulator-only for hardware | Serial JSON, Modbus RTU/TCP drivers, store-and-forward queue. No real RS-485 or board in the loop. |
| Edge builds for arm64 and x86, Ubuntu and Windows | Built (build and packaging), not run on Windows | `scripts/build-edge.sh` makes linux and windows, amd64 and arm64, with install scripts. Windows installer never run on a real machine. |
| Cloud and on-prem server | Built | Docker compose, deploy docs, offline bundle. |
| Air-gapped enterprise deployment | Built | Self-hosted assets, offline bundle, stdlib-only auth. A dry run on a clean VM is still pending. |
| Dashboard to add devices, many sensor types | Built | Onboarding wizard, profiles, bulk CSV, auto-discovery scan (fakes only), 8 widget types, wall mode. |
| Drag-and-drop flows and triggers, multiple flows | Built | Node-graph editor, versions, simulator, import/export, fragments and live subflows. No nested subflows or parameters. |
| MCP server for AI models | Built | 14 tools, read plus drafts, never actuates. Tested with scripted clients, not with a real model. |
| Mobile application | Partial | Responsive web, home-screen manifest, and an offline app shell (service worker, built 2026-10-04): the UI opens with no connection and shows an offline banner. It caches only the static shell; API, auth and health calls are never cached, so no data is available offline. Verified in headless Chrome (install, cache, offline reload). No native app, no push notifications, no offline data or queued writes. |
| Secure by design | Built, unreviewed | mTLS gateways, ACLs, hashed keys, encrypted secrets, audit log, rate limits, default-deny scoping. No external review. |
| Connect STM32 and other devices | Built (serial/direct MQTT), simulator-only for hardware | Firmware examples in connectors.md. |

## Parity targets (ThingsBoard, Siemens Insights Hub, Node-RED, Report Builder)
| Area | Status | Notes |
|---|---|---|
| Multi-tenancy, RBAC, SSO (OIDC), audit log | Built | SAML: use an OIDC bridge (docs/sso-saml.md), native SAML deliberately not built. |
| Customer / sub-customer hierarchy | Built | Read-only scope for devices, alerts, telemetry; default-deny; leak tests. Dashboards and reports can be shared per customer (built 2026-10-04, leak-tested). No scoped flows, assets or branding. |
| Multi-user workspaces: user admin, local sign-in, invitations, session revocation | Built | See saas-design.md. Custom roles (base role minus denied groups, built 2026-10-04). Quotas and usage metering built 2026-10-04 (operator-set limits, trigger-enforced; no billing or payment processor). MFA (TOTP) at local sign-in built 2026-10-04 (opt-in per user, no tenant-wide enforcement, no recovery codes). Opt-in self sign-up built 2026-10-04 (off by default, unverified email, capped). Optional email invites and password reset built 2026-10-04 (need an SMTP relay; tested with a fake mailer only). |
| Dashboards, widgets, data walls, digital twin | Partial | 8 widgets (not 300), twin card is 2.5D SVG, no 3D. |
| Rules, alarms (ack, assign, escalation, on-call, maintenance windows) | Built | SMS (operator HTTP gateway) and Teams channels are optional, fake-tested only. |
| Asset hierarchy, relations, attributes | Built / partial | No relation-driven dashboards. |
| KPIs and derived points | Partial | Formulas, history and widgets; no KPI alerting. |
| Node-RED style flows | Built / partial | Own engine and editor; Node-RED file import is a subset; real Node-RED not embedded; admin-defined custom node types (saved function presets, no SDK or typed parameters). |
| Function node (sandboxed JS) | Built, unreviewed | Off by default, admin only. |
| Report charts: scatter, gauge, pie (HTML) | Built, unit-tested | Rendered and inspected in headless Chrome; not in XLSX/PDF exports; no custom gauge ranges/bands. |
| Report conditional formatting (threshold highlight) | Built, unit-tested | API/definition only; per-metric HTML tables only; no UI control, not in PDF/XLSX. |
| Report row cap (REPORT_MAX_ROWS, HTTP 413) | Built, unit and DB tested | Not load-tested; byte size and PDF/XLSX memory not measured. |
| Report XML export (`format=xml`) | Built, unit and DB tested | Flat series only; UI button not built or screenshotted (no web deps here). |
| Per-product parity matrix (docs/parity-matrix.md) | Doc only | Condensed from competitive-gap.md and report-builder-parity.md; no new claims. No product at parity. |
| Scheduler leader election, two real processes + SIGKILL failover | Built, tested locally | Same host, one Postgres; no partition/frozen-process or multi-host test. |
| Report multivalue device parameter | Built, unit and DB tested | API/URL only; no parameter UI; not cascading. |
| Native Excel charts in XLSX export (line/area/bar/scatter) | Built, unit-tested, rendered in LibreOffice | Not opened in real Excel; flat layout only; max 8 charts; chart reads the avg column. Fixed a stale-pointer bug found while testing (content-types entry was lost). |
| PDF page setup (a4/letter, portrait/landscape) | Built, unit-tested, rendered | API/URL only; default output unchanged; no UI control; no custom margins. |
| KPI/report expression functions (abs, round, sqrt, min, max, clamp) | Built, unit-tested | No if/comparisons; shared by KPI rules and report computed columns; no UI help text yet. |
| Terminal app `scripts/setup.sh` (animated menu, status, health, logs, backup/restore/upgrade, cluster view, single/multi install) | Built, stub-tested | 30 pseudo-terminal checks against stubbed docker/curl; no real Docker, cluster or Windows run. See docs/terminal-app.md. |
| Multi-node HA installer (3 nodes: etcd, Patroni sync replication, HAProxy, keepalived VIP) | Partial, never run | Generator tested (`scripts/test-install-ha.sh`); the rendered stack has not been started on any machine and no failover drill has been run. MQTT brokers are not clustered. See docs/ha-multi-node.md. |
| Report Builder parity: layouts, params, CSV/HTML/PDF/XLSX, schedules, themes, logos, insights, HTML line/area/bar charts | Built / partial | See docs/report-builder-parity.md. No drill-through or subreports, no charts in XLSX, no Word/PowerPoint, no designer surface. |
| Geofences and map | Partial | Hand-entered positions, circular zones, self-hosted tiles. No enter/exit alerts, polygons or GPS tracks. |
| OTA / fleet updates | Partial, unit-tested only | Staged rollout of edge agent releases with signed manifests. No MCU or PLC firmware flashing, no key rotation. |
| White-label | Partial | Name, accent, logo. No custom domain or email templates. |
| Notifications | Partial | SMTP, Slack, webhook, Kafka, AMQP. Microsoft Teams (incoming-webhook card) and SMS (operator's HTTP gateway, `SMS_GATEWAY_URL`) built 2026-10-04, both optional and unused unless configured; tested against local receivers only, never against Microsoft or a real SMS provider. SMS is the generic JSON contract, not a Twilio/Vonage client. |

## Protocols and SCADA connectivity
| Protocol | Status |
|---|---|
| Modbus RTU/TCP, OPC UA (incl. secure modes), SNMP v2c/v3, BACnet/IP, DNP3, IEC 60870-5-104, CoAP, LwM2M object reads, CAN (SocketCAN receive), LoRaWAN ingest, direct MQTT, serial JSON | Simulator-only (serial and Modbus also unit tested; no real devices) |
| IEC 61850 MMS | Simulator-only, labelled partial and unverified |
| Writes on any protocol | Not built. Modbus writes are approval-only and the edge drivers are read-only; control goes through approvals, four-eyes and the edge gate with a simulated actuator. |
| LwM2M server, CAN FD, J1939, DBC import | Not built |

## AI
| Asked for | Status | Notes |
|---|---|---|
| Advanced AI features | Partial | Forecast (Holt-Winters and ridge, backtest gate), anomaly detection, level shifts, correlation and root-cause hints, report insights. Statistical, labelled, never actuate. No pretrained or deep models. |
| AI that can do everything in the software | Partial | The agent calls the whole `/v1` API as the signed-in user; reads run, changes wait for confirmation. It can never approve control, manage users, keys, secrets or switch targets to automatic, by design. Tested against a scripted fake model only. No real model tried. |
| Local AI model and chat panel | Partial | Bundled `ai-runtime` (llama.cpp + Qwen3-1.7B Q4) with a streaming chat side panel, health/metrics and status API. Real model run and measured on the 2 GB dev machine only; the container image is unbuilt (no Docker here). Typed tools, RAG, workflows, remediation, reports and the eval suite are not built yet. See docs/ai-runtime.md and docs/ai-agent-design.md. |
| Safe upgrade and rollback (`scripts/upgrade.sh`) | Simulator-only | Backup-first upgrades, fast-forward only, automatic rollback on apply or health failure, explicit `--rollback`, bundle checksum verification. 15 shell checks with fake docker/curl and a real git remote pass (`scripts/test-upgrade.sh`); never run on a real Docker host. Signed bundles not built. |
| Guided installer (Linux/macOS/WSL `install.sh`, Windows `install.ps1`/`install.bat`) | Partial | Preflight, generated secrets, optional AI model, health wait, first workspace and administrator, works from the air-gapped bundle. `install.sh` logic tested against fake docker/curl (`scripts/test-install.sh`, 19 checks incl. a clock-skew warning); the real compose stack, bundle and Windows script were never run (no Docker or Windows here). See docs/install.md. |
| Control from Slack and email | Simulator-only | Signed, fresh, one-time messages, verified identity linking, YES-code confirmation. No real Slack workspace or mail gateway used. |
| Detailed analysis and better reports | Partial | Insights and period comparison in reports. |

## Data
| Asked for | Status | Notes |
|---|---|---|
| Production time-series store | Partial | Partitioned Postgres with hourly and daily rollups and retention. TimescaleDB and others evaluated, not adopted; no throughput figure measured on target hardware. No compression. |
| Search | Partial | Postgres-based `GET /v1/search`. Optional telemetry index sink to OpenSearch/Elasticsearch (off by default, docs/search-sink.md): simulator-tested only, not run against a real cluster, nothing queries it back. |

## Quality and process
| Item | Status |
|---|---|
| Automated tests | Built, local only |
| CI pipeline defined (go, web, security scan, compose smoke) | Built; not running (Actions quota) |
| Docs (design, security, deployment, hazard analysis, honest gap list, pilot-readiness checklist) | Built |
| Notification channel admin: send test (real delivery path), enable\/disable, delete refused while escalation steps, on-call schedules, reports or flows use it (built 2026-10-04, tested) | Built |
| Startup config audit (`api -check-config`, `STRICT_CONFIG=1`) | Built, tested |
| Security hardening review by an outside party | Not done |
| Load and soak on target hardware, real-device pilots | Not done |

## HexThings rebrand and installers (Oct 5)

- Built: HexThings name and logo (SVG/PNG, favicon, PWA icons), colour terminal installer with plain fallback (tested with a fake docker; real install never run), guided Linux edge installer `edge/packaging/install.sh` (tested with a fake binary only).
- Reports now print the HexThings mark when a tenant has no logo of its own (tenant logo wins; unit-tested decode, DB tests pass). Guided Windows edge installer `install.ps1`/`install.bat` written, never run.
- Technical identifiers (HexmonEdge service, /etc/hexmon, headers, image name) keep the old name for compatibility.
- One-command bootstrap `scripts/get.sh` / `scripts/get.ps1` (public repo only; never run end to end, get.ps1 never run).

- DB-backed Go tests re-run on a throwaway local Postgres 14 (Oct 5): all server packages pass. Remote CI still not run.
- Sites: `POST /v1/sites` (admin) and an inline "Create site" in Add device and Commission a sensor; new workspaces made by tenantctl/the installer get a "Main site". Before this there was no way to create a site in the UI or API. DB-tested; UI not screenshot-tested.
- Local AI install + management CLI (Oct 5): installer downloads Qwen3-1.7B Q4_K_M (sha256 pinned, resumable, opt-out) and auto-connects the assistant (`tenantctl ai-connect`); `hexthings` PowerShell command (status/start/stop/logs/version/update/patch/backup/restore). Model is stock, not trained on the software. Measured here: real llama.cpp build + real model, 1.49 GB peak RSS, ~7 tok/s on 2 CPUs, no GPU. Whole-stack 4-5 GB is an estimate; Docker image build, real download via installer, Windows 5.1 never run. Compose AI limit now 2 GB.
- Assistant fixes (Oct 5): grounding/no-invention rules, UI map, workspace context, short-answer style; chat panel renders headings and tab/pipe tables, plan and steps collapsed; assistant can propose create site/asset/customer/group (confirmation, role-checked, audited); users stay UI-only. Server and web tests pass locally; no real model run; not screenshot-verified; remote CI not run.

- Local assistant context overflow fixed: compact prompt, per-turn tool subset, default context 6144, overflow retry (real model tested via llama-server only).
- Settings > AI providers: saved provider profiles (name, base URL, model, encrypted write-only key per profile), one active, switch from a dropdown, test per profile, edit/delete, built-in local model profile. Admin-only, audited (`ai.profile.*`), off by default. Tested: API integration test with a fake model (key never returned, stored encrypted, switch without re-entering the key); UI type-checked only, not screenshot-tested yet.
- Provider profiles have a capability (auto/small/full): small-model workarounds apply only in small mode; full models get the whole prompt, all tools and no fixed answers (fake-model tests only).
- Terminal: big gradient HexThings wordmark and footer in install.sh/install.ps1/hexthings; interactive hexthings menu, diagnostics, support bundle, maintenance, dev tools and live dashboard (Windows). Tested on Linux against a fake docker only; not on PS 5.1 or real Docker.
- Observability backend: 60 s metrics sampler, redacted warn/error log store, admin-only /v1/system/{health,metrics,logs,prom,support} (see docs/observability.md). Tested with Go integration tests; web System Health and Dev tools pages not built yet; no per-container or MQTT metrics.
- Web: System health page (live SVG charts: API requests/errors/latency, ingest, DB size/connections, alerts, process memory, "what is measured" panel with not-available items) and Dev tools page (log table, level/service/time filters, search, redacted support bundle download). Admin-only. Screenshot-checked in headless Chrome against a real API and Postgres; vitest not added for these pages yet.
- Web: Edge setup wizard (site, serial, MX300/MFM383A template, RS-485 or Modbus TCP settings, claim code via the existing enrollment API, downloadable device YAML, install commands for Linux/Windows, check-in status). Settings generation unit-tested (4 tests); install commands use the configured server address (see docs/server-addresses.md). Whole path never run on a real edge box or meter. NOT built yet: CSV/JSON map import, auto-detect UI, assistant datasheet-to-template, fleet UI with staged rollouts, resources tab, install rollback/preflight upgrades.
- Server addresses: per-workspace and per-site edge server addresses with priority and fallbacks, suggestions from host interfaces, loopback warnings, server-side reachability test (Settings > Server addresses); claim codes and install commands use them. Go tests pass (URL rules, resolution order, admin-only, claim response, check). Install and claim try fallback addresses in order (Go and shell tests; Windows installer parse-checked only). Not built: runtime server switching after enrollment, mDNS in the installer, edge-side reachability proof.
- Register maps: Profiles page can load built-in templates, import CSV/JSON (upload or paste), export CSV/JSON, with row-numbered checks that block saving until fixed (docs/register-maps.md). 6 unit tests; import never auto-saves. Not built: auto-detect suggestions UI, assistant datasheet-to-map, bigger device library, import for non-Modbus protocols.
- Fleet updates page: boxes by site, releases, staged rollouts with progress, start/advance/pause/abort/rollback (docs/fleet.md). Backend engine was already unit-tested; UI helpers tested (3), screenshot-checked. Not built: remote actions on a box, per-box version and last-seen, rollback picker, custom groups.
- Resources page: 9 offline in-app guides with search (docs/in-app-guides.md). 4 unit tests (unique ids, valid links, search, honesty wording). Not built: images in guides, per-page help links, translations.

## 2026-10-07: model-size chooser

Built: shared pinned Apache-2.0 catalog (Qwen3 1.7B/4B/8B, gpt-oss 20B/120B), host
RAM/CPU/disk, sizes, estimated fit warnings, measured-small-only recommendation,
skip, shell/Windows install flags, Windows hexthings menu/command, later replacement
with size+SHA checks and per-model resume. No unattended default AI download; offline
list and airgap network block. Runtime config recorded after verification.
120B is visible but manual-only, not automatically installable. See [model chooser](model-chooser.md).
Tested: 16 shell chooser checks, 14 PowerShell chooser checks (PS7/Linux mocks),
22 installer shell checks, 15 upgrade shell checks; .ps1 parser clean, docsrag test.
Not run: real Windows, real Docker/compose, whole-stack resource benchmark, inference
with any newly listed model, remote CI. RAM estimates are not guarantees. The 1.7B
model measurement predates this unit; larger models remain untested here.

## 2026-10-07: agent/MCP execution boundary hardening

Built: enforce per-turn offered tools, JSON-object/16 KiB argument gate, canonical
repeat signatures, reject oversized batches before execution, cancellation checks,
large catalog local models get full mode unless explicitly overridden. MCP 64 KiB
request cap, strict envelope/protocol and 30-second context. Existing confirmation,
RBAC and four-eyes remain. See [failure matrix](agent-failure-matrix.md).
Tested: full server Go suite without DB, plus API and MCP suites against a fresh real
Postgres database (DB tests ran), scripted provider integration of guard/batch/cancel, policy/registry/LLM/MCP tests. Not run:
real model, real Docker, Windows, distributed/crash recovery, remote CI. Memory/vector
search, durable run recovery and broader edge/report/UI reliability work remain open.

## 2026-10-07: entity search failure boundaries

Built: no-redirect ES client, checked status/request/marshal, 1 MiB reply cap,
query/index validation, tenant integrity for every returned hit, shape/count check.
Global search errors hide backend detail; customer middleware denial pinned and
handler guard added. No customer leak was demonstrated: middleware already denied it.
Tested: fake-ES failure table and API denial/error tests, local server with Postgres;
real ES/OpenSearch not run. Memory/vector retrieval not built or chosen. See
[search reliability and memory direction](search-reliability.md).

## 2026-10-07: explicit private memory, API-first

Built: opt-in per-user/tenant explicit notes in existing Postgres, interactive-only
management/export/delete, 50-note cap and 1-90 day TTL, read-only offline BM25 tool,
current identity/role/scope checks, untrusted provenance warnings, content-free audit.
No automatic chat capture or new vector service. API only, no UI yet. Expired rows
purge on next owner access; backup erasure/background cleanup not built.
Tested: real Postgres owner/tenant/role/scope/disable/delete/expiry/quota and policy
checks; eight docs source-recall queries + unknown/no-evidence and invalid limits.
Not real-model/injection eval or broad semantic benchmark. See [memory](assistant-memory.md).

## 2026-10-07: private memory controls

Built: Assistant page opt-in, explicit note/retention form, disable retrieval, JSON
export, delete review/cancel and visible backend errors. Consent warning names hosted
provider disclosure; expiry-on-access and backup limits remain visible. No chat capture.
Tested: full web suite and production build; React interaction tests and fixture API
render inspection at desktop/mobile widths. Real logged-in browser/API flow and real
model behavior not run. Backend DB tests from the preceding memory unit remain valid.

## 2026-10-07: assistant action outcome truth

Built: pending claims use executing, success requires stored handler result; conservative
unknown for 5xx/cancellation-during-effect/lost result. Unknown autorun stops model loop.
Stale claims displayed unknown after ten minutes, no replay; legacy missing-result rows
converted. Real Postgres concurrent-claim/failure tests, targeted race tests and full
server suite passed on fresh local DB. Not durable resumable runs or response replay;
no real process-kill/partition/distributed/model test. See [action outcomes](assistant-action-outcomes.md).
