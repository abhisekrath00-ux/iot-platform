# Architecture

Full rationale and sources live in the blueprint (Hexmon IoT platform blueprint,
27 Sep 2026). This file is the working reference for the code in this repo, and
the discussion doc for why the system is shaped the way it is.

## Component status (what is implemented in this repo)

| Component | State | Notes |
|---|---|---|
| Edge agent | Implemented | Modbus RTU drivers incl. config-driven `modbus-generic` (fn 1-4, u16..f32/bool, all word orders), SQLite store-and-forward, command allowlist + approval gating, one-time enrollment claim with on-device keygen, `fleetctl` update applier |
| Enrollment & commissioning | Implemented | Claim codes (SHA-256 at rest, expiring, single-use, rate-limited), CSR signing to deployment CA, cert fingerprint per gateway, commissioning wizard across API + edge + ingest (see docs/commissioning.md) |
| Ingest | Implemented | MQTT consumer, idempotent insert, rules + flow evaluation per reading |
| API | Implemented | Devices, telemetry, fleet, audit, rules, notification channels, profiles, reports, flows, dashboards, enrollment, OIDC SSO, search; RBAC (admin/operator/viewer), security headers, rate limits |
| Web dashboard | Implemented | Fleet, devices + 24h charts, onboarding wizard, dashboard builder (KPI/trend widgets + templates), flows + rules, report builder, sensor profiles, commands with approval, audit log, settings |
| MCP server | Implemented, read-only | list-sites, device-health, query-time-series, explain-alert (JWT-scoped); regression suite in `cmd/mcpeval` (docs/mcp-eval.md) |
| Search | Implemented | Elasticsearch, tenant-scoped /v1/search over devices + alerts |
| Broker ACLs | Implemented | Per-gateway Mosquitto ACL file generated from enrollment (docs/broker-acl.md); enable in production via `BROKER_ACL_FILE` |
| Fleet management | Implemented (API) | Releases + staged campaigns with rings, pause/abort, progress tracking (docs/fleet.md); live OTA on real gateways is pilot scope |
| HA | Partial | Redis-backed shared OIDC state; report scheduler and ingest need a lease/leader election before multi-replica |
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
explicit policy. Physical safety gating is in docs/security.md and
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

The API is stateless (sessions in Redis) and scales horizontally; ingest and the
report scheduler are single-leader today (lease election is the known follow-up).
`cmd/loadtest` generates sustained telemetry load against the full path and
`internal/loadstats` reports ingest/query rates; SLO targets live in docs/slo.md.
First live load test runs on the pilot stack; targets are pre-committed so the
pilot either meets them or we fix before scaling.

## Security architecture (summary)

mTLS per gateway, deployment CA via scripts/gen-ca.sh, per-gateway broker ACLs,
OIDC SSO with rate-limited auth, RBAC on every write path, append-only audit log,
secrets only via env/secret files, security headers on the API, dependency
scanning (govulncheck + npm audit) in CI. Full model: docs/security.md.

## Verification status

Everything above labeled Implemented is exercised by Go unit tests, web vitest
suites, and the CI compose smoke that boots the full stack. What CI cannot prove
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
