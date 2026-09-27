# Architecture

Full rationale and sources live in the blueprint (Hexmon IoT platform blueprint,
27 Sep 2026). This file is the working reference for the code in this repo.

## Logical view

```
Energy meter / door sensor / future sensors
        |  RS-485 or isolated USB serial (per device datasheet)
        v
+-------------------------+        outbound MQTT 5 over mTLS        +----------------------+
|  Axon edge gateway      |  ------------------------------------>  |  MQTT broker         |
|  edge-agent (Go)        |                                       |  (Mosquitto pilot)   |
|  serial drivers,        |                                       +----------+-----------+
|  validation, SQLite     |                                                  |
|  store-and-forward      |                                       +----------v-----------+
|  command allowlist      |  <-----------------------------------  |  ingest worker (Go)  |
+-------------------------+        commands (approved, signed)     |  validate + dedupe   |
                                                                    +----------+-----------+
                                                                               |
                                                          +--------------------v--------------------+
                                                          |  PostgreSQL: tenants, sites, gateways,  |
                                                          |  devices, points, telemetry partitions, |
                                                          |  commands, audit                        |
                                                          +--------------------+--------------------+
                                                                               |
                                                          +--------------------v--------------------+
                                                          |  API (Go): authN/Z, devices, telemetry, |
                                                          |  commands+approval, rules, reports,     |
                                                          |  notifications (email/SMTP, Slack)      |
                                                          +--------------------+--------------------+
                                                                               |
                                                          +--------------------v--------------------+
                                                          |  Web (React/TS): onboarding, dashboards,|
                                                          |  report builder, flows, alerts, admin   |
                                                          +-----------------------------------------+
```

## Data model

Tenant -> Site -> Gateway -> physical Endpoint -> logical Device -> Point (metric).
A meter exposes points (kWh, voltage, current); a door contact exposes state.
Device *profiles* carry protocol, register map, scale, unit, poll interval and
validation limits. Profiles make new-device onboarding a UI task, not a code change.

## Telemetry envelope (schema v1)

`tenant_id, site_id, gateway_id, device_id, point_id, event_id, observed_at,
received_at, value, unit, quality, seq, schema_version`

- QoS 1 + app-level `event_id` for dedupe. Never promise exactly-once end to end.
- `tenant_id` is enforced from the gateway's authenticated identity, never from payload.
- `quality` distinguishes measured / estimated / missing / stale. The UI must render it.

## Command path (monitoring + control)

Commands flow on separate topics with a strict envelope:
`request_id, target, action, parameters, approved_by, issued_at, expires_at, policy_version`.

Pipeline: user permission -> policy check -> short-lived command -> gateway allowlist
-> ACK -> measured outcome recorded to audit. Non-idempotent actuation is never retried
without explicit policy. Physical safety gating is in docs/security.md and is a release
gate, not a nice-to-have.

## Technology choices (summary)

| Layer | Choice | Why |
|---|---|---|
| Edge agent | Go, systemd, SQLite queue | small static binary, easy cross-compile for ARM |
| Transport | MQTT 5 over TLS 1.2+, per-gateway identity | proven IoT fit, ACLs, QoS |
| Broker | Mosquitto (pilot) | lean self-hosted; evaluate HA broker before fleet scale |
| Control plane | Go modular monolith + ingest worker | clear boundaries without early microservices |
| Data | PostgreSQL (partitioned telemetry), object storage for exports | on-prem parity, transactional audit |
| Web | React + TypeScript; React Native companion later | shared types, responsive-first |
| Deploy | OCI containers; Compose for lab/small on-prem; Kubernetes later if ops capacity | same artifacts cloud and on-prem |

Change any of these only with a written ADR in docs/adr/.
