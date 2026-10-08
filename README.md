# HexThings

An enterprise IoT platform for energy meters, door sensors and other field devices.
Sensors connect to an edge gateway (Vicharak Axon SBC, any Linux arm64/x86 box, or a Windows PC) over UART/USB serial, Modbus RTU/TCP or OPC UA;
the edge agent buffers and forwards telemetry over MQTT (mTLS) to the control plane,
which serves a web dashboard, rules/flows, alerts, reports, fleet rollout and
AI-assisted diagnosis via MCP. The same container stack deploys to cloud, customer
premises, or fully air-gapped sites.

Project tracking: [Trello board](https://trello.com/b/ErbxnFXn/hexmon-iot-platform)

## Feature checklist

Status labels: **Verified** = exercised by automated tests / CI on this repo.
**Pilot** = implemented end to end, but needs the first real hardware or site
deployment to be called proven. **Config** = behavior driven by configuration,
no code change needed.

### Devices and data
| Feature | What you get | Status |
|---|---|---|
| Live monitoring | Latest values, quality flags, 24h trends per device/point; KPI cards | Verified (simulated telemetry); real sensors are Pilot |
| Multi-sensor onboarding | `modbus-generic` driver + device profiles; new sensor types added by config/UI, not code | Verified (Config) |
| UI device onboarding | Add-device page, one-time enrollment tokens, commissioning wizard (API + edge claim + ingest validation) | Verified; wizard visual pass on a real deployment is Pilot |
| Connectors | Modbus RTU, **Modbus TCP**, **OPC UA** (SCADA/PLC), **STM32/Arduino/ESP32 UART** - see [docs/connectors.md](docs/connectors.md) | Verified against in-process Modbus TCP and OPC UA servers; real hardware is Pilot |
| Edge on Ubuntu + Windows | Static agent for linux/windows x amd64/arm64, installers, Windows service - see [docs/edge-install.md](docs/edge-install.md) | Cross-compiled in CI; Windows service + arm64 need a first pilot install |
| SCADA/BI export | Bounded telemetry CSV export, report CSV/HTML download, JSON APIs | Verified (unit-tested renderers); live DB paths exercised by compose smoke only in part |
| Device profiles | Profile CRUD (`/v1/profiles`, Profiles page) mapping points, units, scaling | Verified |

### Control and automation
| Feature | What you get | Status |
|---|---|---|
| Physical control | Command requests with mandatory approval (four-eyes: requester != approver), edge executor allowlist | Verified; physical actuation is Pilot (needs hardware) |
| Workflows (flows) | Flow definitions with draft/publish, version history, rollback, dry-run simulate | Verified |
| Rules | Threshold/rule evaluation against live telemetry | Verified |
| Alerts | Severity, acknowledge/resolve lifecycle with notes and status filter, fan-out to notification channels | Verified |
| Flow cooldown | Optional per-flow quiet period so a stuck sensor does not flood channels | Verified (not a latch: does not wait for the value to recover) |

### Dashboards and reporting
| Feature | What you get | Status |
|---|---|---|
| Custom dashboards | Builder with KPI, gauge, bar, status and 24h-trend widgets, thresholds, templates, drag-and-drop layout and sizing, wall/TV mode | Verified (layout logic unit-tested; drag reorder and wall mode covered by a manual browser e2e script, `web/e2e/dashboard.e2e.cjs` (needs a running stack and Chrome, not run in CI); wall mode rotates dashboards every 30s) |
| Device tags and fleet search | Up to 20 tags per device, search and filter the fleet | Verified |
| Device health and digital twin | Explainable 0-100 health score and a 2.5D twin card per device | Verified (score rules unit-tested; twin is an SVG/CSS card, not a 3D model) |
| Product tour | First-run guided tour, restartable | Verified (logic); visual check by screenshot |
| Report builder | Report definitions, on-demand runs, scheduled cron runner | Verified |
| Audit logs | Append-only audit trail (who/what/when), admin-only API + Audit page | Verified |

### Platform and integration
| Feature | What you get | Status |
|---|---|---|
| Notifications | SMTP email and Slack channels (`/v1/notifications/channels`) | Verified end to end with test servers; real deliverability is Pilot (needs site SMTP/Slack creds) |
| RBAC + SSO | admin/operator/viewer roles on every write path, OIDC login, rate-limited auth | Verified |
| MCP (AI integration) | MCP server with 7 tools (sites, device health, time series, alert explain, list devices, list alerts, aggregate) plus `mcpeval` regression suite | Verified (eval suite, DB-backed tool tests) |
| API keys | Hashed, tenant-scoped, viewer/operator only, expiring, revocable keys for SCADA/BI | Verified (no per-key endpoint scopes yet) |
| Retention and rollups | Hourly rollups, optional raw purge (`RAW_RETENTION_DAYS`), rollup API | Verified (report builder uses rollups after purge; 15-minute buckets are raw-only) |
| Search | Tenant-scoped Elasticsearch over devices and alerts | Verified in compose |
| Fleet management | Releases and staged rollout campaigns (rings, pause/abort), edge `fleetctl` applier | API Verified; OTA on a live gateway is Pilot |
| Broker security | Per-gateway Mosquitto ACLs generated from enrollment | Verified; production enablement documented |

### Deployment and operations
| Feature | What you get | Status |
|---|---|---|
| Containerized deploy | One `docker compose up` brings up Postgres, Mosquitto, API, ingest, Elasticsearch, MCP, web | Verified in CI compose smoke |
| Air-gapped install | `scripts/airgap-bundle.sh` + `install.sh`, no internet needed at the site | Scripted and documented; full on-site rehearsal is Pilot |
| HA | Report scheduler leader election (Postgres advisory lock, tested incl. failover); ingest scale-out via MQTT shared subscriptions (opt-in) | Leader election Verified; multi-replica ingest not load-tested |
| Scale | Stateless API (horizontal scale), `cmd/loadtest` harness, runtime load stats, SLO doc | Harness verified; first live load test on pilot stack pending |
| Backup/restore | `scripts/backup.sh` / `restore.sh` + runbook | Scripted and documented |
| CA/PKI tooling | `scripts/gen-ca.sh` for site CA and mTLS certs | Verified |

## Repository layout

| Path      | What it is |
|-----------|------------|
| `edge/`   | Go edge agent (Linux + Windows, x86_64 + arm64): Modbus RTU/TCP, OPC UA and serial drivers, local SQLite queue, mTLS MQTT publisher, claim/commissioning, command executor (allowlisted), fleet applier |
| `server/` | Go control plane: `cmd/api` (REST, auth, commands, dashboards, reports, flows, fleet, notifications), `cmd/ingest` (MQTT -> Postgres), `cmd/mcp`, `cmd/loadtest`, `cmd/mcpeval`, SQL migrations |
| `web/`    | React + TypeScript UI: fleet, devices, onboarding, dashboards, flows, alerts, control, reports, profiles, audit, settings |
| `deploy/` | Mosquitto broker config and deployment assets |
| `scripts/`| Air-gap bundle/install, backup/restore, CA generation |
| `docs/`   | Architecture, setup, deployment, testing, security, air-gap, commissioning, fleet, hazard analysis, SLOs |

## Quickstart (lab)

```bash
cp .env.example .env          # adjust passwords
docker compose up --build     # postgres, mosquitto, api, ingest, elasticsearch, mcp, web
# web dashboard: http://localhost:8080  ·  API: http://localhost:8000
```

The edge agent runs on the Axon gateway, not in the lab compose stack; see
`docs/setup.md`. For a site with no internet, build the bundle on a connected
machine and install offline per `docs/airgap.md`.

## Documentation

- [Architecture](docs/architecture.md) — system design and component discussion
- [Install (one command)](docs/install.md) · [Local setup](docs/setup.md) · [Deployment (cloud & on-prem)](docs/deployment.md) · [Air-gapped install](docs/airgap.md)
- [Testing](docs/testing.md) — what CI runs and how to run it locally
- [Security model](docs/security.md) — mTLS, RBAC, approval gating, hazard analysis
- [Connectors (STM32, Modbus, OPC UA, SCADA)](docs/connectors.md) · [Direct MQTT devices (design)](docs/mqtt-direct.md) · [Competitive gap analysis](docs/competitive-gap.md) · [Edge install (Ubuntu/Windows)](docs/edge-install.md)
- [Commissioning](docs/commissioning.md) · [Fleet rollout](docs/fleet.md) · [Backup/restore](docs/backup-restore.md) · [First-run credentials](docs/first-run-credentials.md) · [Try the latest build / update an old install](docs/try-and-upgrade.md)
- [MCP evaluation](docs/mcp-eval.md) · [SLOs](docs/slo.md) · [Contributing](docs/contributing.md)

## Status

Working software, CI-green: the full telemetry path (edge -> MQTT -> ingest ->
Postgres -> API -> web) plus control gating, dashboards, flows, reports, fleet
rollout and MCP are implemented and covered by automated tests. Items labeled
**Pilot** above need the first real gateway, meters and site rollout before they
can be called proven in production. Physical control is gated behind the
approval flow and policy checks described in `docs/security.md` — do not bypass.
