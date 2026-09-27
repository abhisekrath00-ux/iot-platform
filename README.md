# Hexmon IoT Platform

An enterprise IoT platform for energy meters, door sensors and other field devices.
Sensors connect to a Vicharak Axon SBC (edge gateway) over UART/USB serial; the edge
agent buffers and forwards telemetry over MQTT (mTLS) to the control plane, which
serves a web dashboard, rules/flows, alerts, reports and (later) AI-assisted
diagnosis via MCP. Deployable to cloud or customer premises with the same
container stack.

## Repository layout

| Path      | What it is |
|-----------|------------|
| `edge/`   | Go edge agent: serial drivers, local SQLite queue, MQTT publisher, command executor (allowlisted) |
| `server/` | Go control plane: `cmd/api` (REST API, auth, commands, notifications) and `cmd/ingest` (MQTT -> Postgres), SQL migrations |
| `web/`    | React + TypeScript dashboard: device onboarding, dashboards, report builder, flows, alerts, admin |
| `deploy/` | Mosquitto broker config and deployment assets |
| `docs/`   | Architecture, setup, deployment, testing, security, contributing |

## Quickstart (lab)

```bash
cp .env.example .env          # adjust passwords
docker compose up --build     # postgres, mosquitto, api, ingest, web
# web dashboard: http://localhost:8080  ·  API: http://localhost:8000
```

The edge agent runs on the Axon gateway, not in the lab compose stack; see
`edge/README` notes in `docs/setup.md`.

## Documentation

- [Architecture](docs/architecture.md)
- [Local setup](docs/setup.md)
- [Deployment (cloud & on-prem)](docs/deployment.md)
- [Testing](docs/testing.md)
- [Security model](docs/security.md)
- [Contributing](docs/contributing.md)

## Status

Early scaffold. Telemetry path (edge -> MQTT -> ingest -> Postgres -> API -> web)
is the first milestone. Physical control is gated behind the approval flow and
policy checks described in `docs/security.md` — do not bypass.
