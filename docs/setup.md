# Local setup

## Prerequisites

- Docker + Docker Compose v2
- Go 1.23+ (for running services outside containers)
- Node 20+ (for web development)
- For edge work: a Vicharak Axon (Ubuntu MATE) or any Linux SBC, plus the
  USB/UART converter and meter/sensor per their datasheets.

## Run the platform (lab)

```bash
cp .env.example .env
docker compose up --build
```

- Web dashboard: http://localhost:8080
- API: http://localhost:8000/healthz
- Postgres: localhost:5432 (credentials from .env)
- MQTT: localhost:1883 (lab, anonymous; production config in deploy/mosquitto)

Migrations in `server/migrations` run automatically at API startup.

## Run services natively (development)

```bash
# control plane API
cd server && go run ./cmd/api
# ingest worker
cd server && go run ./cmd/ingest
# web
cd web && npm install && npm run dev
```

## Edge agent on an Axon

```bash
cd edge
GOOS=linux GOARCH=arm64 go build -o edge-agent ./cmd/edge-agent
scp edge-agent axon:/usr/local/bin/
```

On the gateway, configure `/etc/hexmon/edge-agent.yaml` (see
`edge/cmd/edge-agent` flags and `internal/config`) and install the systemd unit
from `docs/deployment.md`. The agent:

1. opens the configured serial port (device profile: baud/parity/register map),
2. validates readings (range, unit, CRC handled by driver),
3. appends to a local SQLite queue (survives WAN loss),
4. publishes over MQTT with mTLS using the gateway's own certificate,
5. subscribes to its command topic and executes only allowlisted, unexpired,
   approved commands.

## Seeding demo data

`docker compose exec api /bin/api --seed-demo` creates a demo tenant, site,
gateway, one energy meter (kWh/V/A points) and one door sensor so the dashboard
has content on first run.
