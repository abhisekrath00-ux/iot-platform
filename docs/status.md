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
| Report Builder parity: layouts, params, CSV/HTML/PDF/XLSX, schedules, themes, logos, insights | Built / partial | No drill-through or subreports, charts not rendered into delivered files, no Word/PowerPoint. |
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
| Guided installer (Linux/macOS/WSL `install.sh`, Windows `install.ps1`/`install.bat`) | Partial | Preflight, generated secrets, optional AI model, health wait, first workspace and administrator, works from the air-gapped bundle. `install.sh` logic tested against fake docker/curl (`scripts/test-install.sh`); the real compose stack, bundle and Windows script were never run (no Docker or Windows here). See docs/install.md. |
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
