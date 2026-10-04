# Feature status: what was asked, what exists

Audit of 2026-10-04, written from the code and docs in this repo, not from memory. One label per row.

- **Built**: implemented, with automated tests that run against a real Postgres or in-process fakes.
- **Partial**: a real but narrower version; the limit is stated.
- **Simulator-only**: code exists and passes tests against a simulator or fake that I wrote from the spec. Never run against a real device, broker, model, Slack workspace or mail gateway.
- **Not built**: does not exist.

Across everything: about 420 Go tests and 51 web tests pass locally (full gate plus an empty-database run before each push). **Remote CI has not run since the GitHub Actions minutes ran out, so nothing here is remote-verified.** No outside security review has been done. UI pages were checked in headless Chrome by hand on a subset, not by an automated browser suite.

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
| Mobile application | Partial | Responsive web plus home-screen manifest. No native app, no push, no offline. |
| Secure by design | Built, unreviewed | mTLS gateways, ACLs, hashed keys, encrypted secrets, audit log, rate limits, default-deny scoping. No external review. |
| Connect STM32 and other devices | Built (serial/direct MQTT), simulator-only for hardware | Firmware examples in connectors.md. |

## Parity targets (ThingsBoard, Siemens Insights Hub, Node-RED, Report Builder)
| Area | Status | Notes |
|---|---|---|
| Multi-tenancy, RBAC, SSO (OIDC), audit log | Built | SAML not built. |
| Customer / sub-customer hierarchy | Built | Read-only scope for devices, alerts, telemetry; default-deny; leak tests. No scoped dashboards, reports, flows or branding. |
| Multi-user workspaces: user admin, local sign-in, invitations, session revocation | Built | See saas-design.md. Custom roles (base role minus denied groups, built 2026-10-04). No MFA at sign-in, email invites or reset, quotas or billing, public self sign-up. |
| Dashboards, widgets, data walls, digital twin | Partial | 8 widgets (not 300), twin card is 2.5D SVG, no 3D. |
| Rules, alarms (ack, assign, escalation, on-call, maintenance windows) | Built | No SMS or Teams channels. |
| Asset hierarchy, relations, attributes | Built / partial | No relation-driven dashboards. |
| KPIs and derived points | Partial | Formulas, history and widgets; no KPI alerting. |
| Node-RED style flows | Built / partial | Own engine and editor; Node-RED file import is a subset; real Node-RED not embedded; no custom node SDK. |
| Function node (sandboxed JS) | Built, unreviewed | Off by default, admin only. |
| Report Builder parity: layouts, params, CSV/HTML/PDF/XLSX, schedules, themes, logos, insights | Built / partial | No drill-through or subreports, charts not rendered into delivered files, no Word/PowerPoint. |
| Geofences and map | Partial | Hand-entered positions, circular zones, self-hosted tiles. No enter/exit alerts, polygons or GPS tracks. |
| OTA / fleet updates | Partial, unit-tested only | Staged rollout of edge agent releases with signed manifests. No MCU or PLC firmware flashing, no key rotation. |
| White-label | Partial | Name, accent, logo. No custom domain or email templates. |
| Notifications | Partial | SMTP, Slack, webhook, Kafka, AMQP. No SMS or Teams. |

## Protocols and SCADA connectivity
| Protocol | Status |
|---|---|
| Modbus RTU/TCP, OPC UA (incl. secure modes), SNMP v2c/v3, BACnet/IP, DNP3, IEC 60870-5-104, CoAP, LwM2M object reads, CAN (SocketCAN receive), LoRaWAN ingest, direct MQTT, serial JSON | Simulator-only (serial and Modbus also unit tested; no real devices) |
| IEC 61850 MMS | Simulator-only, labelled partial and unverified |
| Writes on any protocol | Not built. Modbus writes are approval-only and the edge drivers are read-only; control goes through approvals, four-eyes and the edge gate with a simulated actuator. |
| LwM2M server, CAN FD, J1939, DBC import, SAML | Not built |

## AI
| Asked for | Status | Notes |
|---|---|---|
| Advanced AI features | Partial | Forecast (Holt-Winters and ridge, backtest gate), anomaly detection, level shifts, correlation and root-cause hints, report insights. Statistical, labelled, never actuate. No pretrained or deep models. |
| AI that can do everything in the software | Partial | The agent calls the whole `/v1` API as the signed-in user; reads run, changes wait for confirmation. It can never approve control, manage users, keys, secrets or switch targets to automatic, by design. Tested against a scripted fake model only. No real model tried. |
| Control from Slack and email | Simulator-only | Signed, fresh, one-time messages, verified identity linking, YES-code confirmation. No real Slack workspace or mail gateway used. |
| Detailed analysis and better reports | Partial | Insights and period comparison in reports. |

## Data
| Asked for | Status | Notes |
|---|---|---|
| Production time-series store | Partial | Partitioned Postgres with hourly and daily rollups and retention. TimescaleDB and others evaluated, not adopted; no throughput figure measured on target hardware. No compression. |
| Search | Partial | Postgres-based `GET /v1/search`. Elasticsearch/OpenSearch not built. |

## Quality and process
| Item | Status |
|---|---|
| Automated tests | Built, local only |
| CI pipeline defined (go, web, security scan, compose smoke) | Built; not running (Actions quota) |
| Docs (design, security, deployment, hazard analysis, honest gap list) | Built |
| Security hardening review by an outside party | Not done |
| Load and soak on target hardware, real-device pilots | Not done |
