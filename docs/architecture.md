# Architecture

Full rationale and sources live in the blueprint (Hexmon IoT platform blueprint,
27 Sep 2026). This file is the working reference for the code in this repo, and
the discussion doc for why the system is shaped the way it is.

## Component status (what is implemented in this repo)

| Component | State | Notes |
|---|---|---|
| Edge agent | Implemented | Runs on Linux and Windows (x86_64, arm64; Windows service). Drivers: Modbus RTU, Modbus TCP, OPC UA (secure modes), STM32/Arduino/ESP32 serial lines (docs/connectors.md); Modbus RTU drivers incl. config-driven `modbus-generic` (fn 1-4, u16..f32/bool, all word orders), SQLite store-and-forward, command allowlist + approval gating, one-time enrollment claim with on-device keygen, `fleetctl` update applier |
| Enrollment & commissioning | Implemented | Claim codes (SHA-256 at rest, expiring, single-use, rate-limited), CSR signing to deployment CA, cert fingerprint per gateway, commissioning wizard across API + edge + ingest (see docs/commissioning.md) |
| Ingest | Implemented | MQTT consumer, idempotent insert, rules + flow evaluation per reading |
| API | Implemented | Devices, telemetry, fleet, audit, rules, notification channels, profiles, reports, flows, dashboards, enrollment, OIDC SSO, search; RBAC (admin/operator/viewer), security headers, rate limits |
| Web dashboard | Implemented | Apple-style light/dark design system (self-hosted, air-gap safe), fleet overview, devices + 24h charts, onboarding wizard, dashboard builder (KPI/trend widgets + templates), flows + rules, report builder (point picker, live preview, presets, CSV/HTML export), protocol-aware sensor profiles, commands with approval, audit log, settings |
| MCP server | Implemented, read-only | list-sites, device-health, query-time-series, explain-alert, list_devices, list_alerts, aggregate_time_series (JWT-scoped); regression suite in `cmd/mcpeval` (docs/mcp-eval.md) |
| Search | Implemented | Elasticsearch, tenant-scoped /v1/search over devices + alerts |
| Broker ACLs | Implemented | Per-gateway Mosquitto ACL file generated from enrollment (docs/broker-acl.md); enable in production via `BROKER_ACL_FILE` |
| Fleet management | Implemented (API) | Releases + staged campaigns with rings, pause/abort, progress tracking (docs/fleet.md); live OTA on real gateways is pilot scope |
| HA | Partial | Redis-backed shared OIDC state; report scheduler uses Postgres advisory-lock leader election (`internal/leader`, tested with competing contenders + failover); ingest can scale out via MQTT shared subscriptions (`INGEST_SHARED_GROUP`, opt-in, needs broker ACL for `$share/`), not load-tested multi-replica; broker and Postgres HA are deployment-level and not shipped |
| Physical actuation | Gated | Hazard analysis done (docs/hazard-analysis.md); command path enforces approval gating; actuation stays disabled at sites until interlock review signs off |

## Logical view

```
Energy meter / door sensor / future sensors
        |  RS-485 or isolated USB serial (per device datasheet)
        v
+-------------------------+        outbound MQTT 5 over mTLS        +----------------------+
|  Axon edge gateway      |  ------------------------------------>  |  MQTT broker         |
|  edge-agent (Go)        |                                       |  (Mosquitto pilot,   |
|  serial drivers,        |                                       |  per-gateway ACLs)   |
|  validation, SQLite     |                                       +----------+-----------+
|  store-and-forward      |                                                  |
|  command allowlist      |                                       +----------v-----------+
|  fleetctl applier       |  <-----------------------------------  |  ingest worker (Go)  |
+-------------------------+        commands (approved, signed)     |  validate + dedupe   |
        ^                                                           +----------+-----------+
        |  fleet releases (staged campaigns)                                   |
        |                                                          +-----------v-----------+
        +----------------------------------------------------------|  PostgreSQL: tenants, |
                                                                   |  sites, gateways,     |
                                                                   |  devices, points,     |
                                                                   |  telemetry, commands, |
                                                                   |  audit (append-only)  |
                                                                   +-----------+-----------+
                                                                               |
                                        +--------------------------------------+----------------------+
                                        |                                      |                      |
                          +-------------v-------------+          +-------------v---------+  +---------v--------+
                          |  API (Go): authN/Z, RBAC, |          |  Elasticsearch:       |  |  MCP server:     |
                          |  devices, telemetry,      |          |  tenant-scoped search |  |  read-only tools |
                          |  commands+approval, flows,|          +-----------------------+  |  for AI clients  |
                          |  reports, notifications   |                                     +------------------+
                          |  (SMTP, Slack)            |
                          +-------------+-------------+
                                        |
                          +-------------v-------------+
                          |  Web (React/TS): fleet,   |
                          |  onboarding, dashboards,  |
                          |  flows, reports, control, |
                          |  audit, admin             |
                          +---------------------------+
```

## Data model

Tenant -> Site -> Gateway -> physical Endpoint -> logical Device -> Point (metric).
A meter exposes points (kWh, voltage, current); a door contact exposes state.
Device *profiles* carry protocol, register map, scale, unit, poll interval and
validation limits. Profiles make new-device onboarding a UI task, not a code change —
this is the answer to the multi-sensor requirement: adding a sensor model is a
profile row (UI or seed), never a driver change, as long as it speaks Modbus.

## Telemetry envelope (schema v1)

`tenant_id, site_id, gateway_id, device_id, point_id, event_id, observed_at,
received_at, value, unit, quality, seq, schema_version`

- QoS 1 + app-level `event_id` for dedupe. Never promise exactly-once end to end.
- `tenant_id` is enforced from the gateway's authenticated identity, never from payload.
- `quality` distinguishes measured / estimated / missing / stale. The UI renders it.

## Command path (monitoring + control)

Commands flow on separate topics with a strict envelope:
`request_id, target, action, parameters, approved_by, issued_at, expires_at, policy_version`.

Pipeline: user permission -> policy check -> short-lived command -> gateway allowlist
-> ACK -> measured outcome recorded to audit. Four-eyes is enforced: the approver
must differ from the requester. Non-idempotent actuation is never retried without
explicit policy.

Implemented: on approval the API publishes the envelope (non-retained, QoS 1) to
`t/<tenant>/g/<gateway>/cmd` and sets status `sent`, or `failed` if the broker
refused it; each outcome is audited. Edge side: `edge/internal/cmdexec` is a unit-tested gate (fail-closed
allowlist, `expires_at`, max lifetime, clock skew, single-use `request_id`, strict
envelope). Not implemented: subscribing to the topic, driving actuators and the ACK. Until it
exists a `sent` command is not executed by anything, and status never reaches
`acked`. Physical safety gating is in docs/security.md and
docs/hazard-analysis.md and is a release gate, not a nice-to-have.

## Commissioning and fleet lifecycle

A new gateway claims with a one-time code minted in the UI, generates its keypair
on-device, submits a CSR, and receives a deployment-CA-signed cert bound to its
fingerprint. The commissioning wizard walks an operator through claim -> connect ->
first-telemetry validation; ingest confirms the first reading. Once enrolled, the
gateway is managed through fleet campaigns: a release (artifact + version) is
rolled out through rings (canary -> broad), with pause and abort, and the edge
`fleetctl` applies updates and reports progress. Details: docs/commissioning.md,
docs/fleet.md.

## Deployment topologies

One container stack, three shapes:

1. **Lab/cloud** — `docker compose up`: Postgres, Mosquitto, API, ingest,
   Elasticsearch, MCP, web. Used by CI compose-smoke on every push.
2. **On-prem** — same compose stack behind the customer's TLS termination; backup
   via scripts/backup.sh.
3. **Air-gapped** — scripts/airgap-bundle.sh packages images + compose + config on
   a connected machine; scripts/airgap-install.sh installs offline at the site
   (docs/airgap.md). No phone-home anywhere in the stack.

## Scale and performance

The API is stateless (sessions in Redis) and scales horizontally. The report scheduler runs on
one replica at a time (Postgres advisory lock, automatic failover). Ingest replicas split the load
with MQTT shared subscriptions when `INGEST_SHARED_GROUP` is set; that path has unit tests for topic
wiring but has not been load-tested with several replicas.
`cmd/loadtest` generates sustained telemetry load against the full path and
`internal/loadstats` reports ingest/query rates; SLO targets live in docs/slo.md.
First live load test runs on the pilot stack; targets are pre-committed so the
pilot either meets them or we fix before scaling.

## Security architecture (summary)

mTLS per gateway, deployment CA via scripts/gen-ca.sh, per-gateway broker ACLs,
OIDC SSO with rate-limited auth, RBAC on every write path, append-only audit log,
secrets only via env/secret files, security headers on the API, dependency
scanning (govulncheck + npm audit) in CI. Full model: docs/security.md.

## Data path for a new device (config, not code)

Profile (protocol + points) -> commissioning wizard stores the device with its connection
settings (`devices.config.connection`, validated by a per-driver whitelist) -> `GET
/v1/gateways/{id}/edge-config` renders the agent YAML (or push it as a fleet config release) ->
agent polls with the matching driver -> telemetry over MQTT mTLS -> ingest -> API/UI/reports/exports.
`points` are keyed per device (migration 0010); the same point name can exist on many devices.

## Verification status

Everything above labeled Implemented is exercised by Go unit tests, web vitest
suites, API integration tests on a real Postgres (tenant isolation, profile -> edge config,
reports, export), driver tests against in-process Modbus TCP and OPC UA servers, and the CI
compose smoke that boots the full stack. What CI cannot prove
is hardware reality: real Modbus meters, UART timing, OTA on a live gateway, and
SMTP/Slack deliverability with site credentials. Those are explicitly pilot-scope
and tracked on the Trello board.

## Technology choices (summary)

| Layer | Choice | Why |
|---|---|---|
| Edge agent | Go, systemd, SQLite queue | small static binary, easy cross-compile for ARM |
| Transport | MQTT 5 over TLS 1.2+, per-gateway identity | proven IoT fit, ACLs, QoS |
| Broker | Mosquitto (pilot) | lean self-hosted; evaluate HA broker before fleet scale |
| Control plane | Go modular monolith + ingest worker | clear boundaries without early microservices |
| Data | PostgreSQL (partitioned telemetry), object storage for exports | on-prem parity, transactional audit |
| Search | Elasticsearch 8 | tenant-scoped full-text over devices/alerts |
| Web | React + TypeScript; React Native companion later | shared types, responsive-first |
| Deploy | OCI containers; Compose for lab/small on-prem; Kubernetes later if ops capacity | same artifacts cloud, on-prem and air-gapped |

Change any of these only with a written ADR in docs/adr/.

## Read caching

Hot read endpoints (`/v1/fleet`, `/v1/telemetry/latest`, `/v1/devices`, `/v1/points`, `/v1/sites`, `/v1/profiles`) sit behind a small in-process response cache (`server/internal/respcache`): tenant-scoped keys, per-endpoint TTL (2 s for live values, 10-30 s for configuration lists), single-flight on concurrent misses, a hard entry cap, and only `200` GET responses stored. Any successful non-GET request drops that tenant's entries. Responses carry `X-Cache: HIT|MISS`.

Limits: the cache is per API replica, so with several replicas a read can be as old as its TTL from any replica (a write invalidates only the replica that served it). That is why TTLs are seconds. A shared cache (Redis is already optional for OIDC state) is the next step if replicas multiply; it is not built.

## Dashboard widgets

Widgets are stored as JSON in the dashboard layout, so adding a type needs no migration. Types: live value (KPI), gauge, trend (24 h line), bars (all points of a device) and device status. Gauge, KPI and bars accept `min`, `max`, `warn` and `crit`; the colour comes from `web/src/lib/widgets.ts` (unit-tested: threshold state, gauge fraction, online/offline freshness). Layout is an auto-fill grid; there is no drag-and-drop or per-widget time-window picker yet.

## Report aggregation

Reports bucket telemetry by 15 minutes, hour, day or week (`report.BucketExpr`, a fixed whitelist spliced into SQL, never user text). Each bucket carries avg, min, max, sum and sample count; the HTML report adds an overall row per metric (count-weighted average, min, max, sum). CSV gains a trailing `sum` column. Covered by unit tests and a real-Postgres test of every bucket size.

## Alert lifecycle

Alerts move `open -> acknowledged -> resolved` (resolve is allowed from open or acknowledged; resolved is terminal).
`POST /v1/alerts/{id}/ack` and `/resolve` require the operator role or above and record who and when.
`POST /v1/alerts/{id}/comments` adds a note to the trail; `GET /v1/alerts/{id}` returns the alert with its notes.
`GET /v1/alerts?status=open|acknowledged|resolved` filters the list. All queries are tenant scoped.

## Device tags and fleet search

Devices carry up to 20 lowercase tags (`a-z 0-9 . _ : -`, max 32 chars). `PUT /v1/devices/{id}/tags` (operator+, audited, tenant scoped) replaces them.
`GET /v1/devices?q=&tag=&gateway_id=` filters the fleet (substring on name/id/profile, exact tag, gateway); results are capped at 1000.

## Device health and digital twin

`GET /v1/devices/{id}/health` returns a 0-100 score, a status (healthy, degraded, critical, offline) and the factors behind it.
Rules live in `server/internal/health` (pure, unit tested): freshness of the newest sample relative to the device's poll interval (weight 50), share of recent samples with `measured` quality (30), and gateway link (20). Stale data is never reported healthy.
The device page shows a tilting twin of the device (SVG/CSS, no external assets, respects reduced motion) with live readouts and an animated health ring. This is a 2.5D card, not a 3D model of the real hardware.

## Dashboard layout and wall mode

Dashboards use a 4-column grid (2 on narrow screens). In edit mode widgets can be dragged to reorder (native HTML5 drag and drop, no extra dependency) or moved with the arrow buttons, and resized 1-4 columns with the +/- buttons. The layout is stored in the dashboard JSON (`span` per widget, order = array order); `spanOf` and `moveItem` in `web/src/lib/widgets.ts` are unit tested.
Wall mode hides navigation and editing chrome, uses larger values and frosted cards, requests browser fullscreen where allowed and exits on Esc. With two or more dashboards that have widgets it rotates between them every 30 seconds. Touch drag-and-drop is not supported (the arrow buttons work on touch).

## Product tour

First sign-in shows an 8-step spotlight tour of the sidebar (Fleet to Settings). Skip or Esc ends it and it stays dismissed (`localStorage` key `hexmon-tour-done`); "Take the tour" in the sidebar restarts it. Steps and progress logic live in `web/src/lib/tour.ts` with unit tests. No external assets.

## Retention and rollups

A leader-elected job (advisory lock, one replica at a time) runs hourly. It rolls the last 48 hours of raw telemetry into `telemetry_rollup_hourly` (count, sum, min, max per tenant/device/point/hour, measured and estimated samples only). Rollups are recomputed for whole hours, so re-running is safe.
`RAW_RETENTION_DAYS` (default 0 = keep everything) enables purging: raw rows older than the window are deleted only after their hours are rolled up, in batches of 5000. Rollups are never purged.
`GET /v1/telemetry/rollup?device_id=&point_id=&from=&to=` returns hourly aggregates (default last 30 days, max span 2 years) and keeps answering after raw data is gone.
The report builder (hour, day, week buckets) reads rollups for hours whose raw rows were purged and raw rows from the first retained hour on, with no double counting; 15-minute buckets read raw only. Rollups count measured and estimated samples only, while raw report reads count every quality, so a report can differ slightly across the purge boundary if a device sent `missing`/`stale` samples. Not built yet: daily rollups, per-tenant retention settings, routing the 24h series API to rollups, and partition dropping for very large installs.

## Flow cooldown (dedupe)

A flow definition may set `cooldown_seconds` (0-86400). After a run notifies, further matching readings are recorded in `flow_runs` as `suppressed_cooldown` until the cooldown passes, so a sensor stuck above its threshold does not flood email or Slack. Default 0 keeps the old behaviour (notify on every match). Cooldown is per flow, not per device. It is not a latch: it does not wait for the reading to return to normal.
