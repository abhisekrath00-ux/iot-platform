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

## Health results are complete or explicitly incomplete (2026-10-30)

On Linux/WSL, `bash scripts/setup.sh health` requires direct HTTP 2xx for API and
web, an MQTT TCP connection, and successful Compose Postgres `pg_isready` before
returning zero. A redirect is not readiness. Missing/unreachable Docker or failed
Postgres checks return nonzero with `NOT VERIFIED` and a next step, even if API and
web answer. This prevents a skipped database check being shown as a healthy stack.
For HA/Patroni deployments, use the cluster view and the HA drill as well; the
single-node Compose database check is not multi-node failover evidence. MQTT here
is only a TCP connectivity check, not authenticated publish/subscribe acceptance.

Verification: Linux terminal/PTTY and prerequisite decision tests use stubbed
Docker/curl, with a real local TCP listener; they are not Docker installation
proof. The freshDB endpoint test actually boots the Go API, migrates, seeds through
tenantctl, replaces first-run credentials and verifies old-token rejection. A
local prerequisite check reported Docker absent and warned about ~2 GB RAM; no
Docker install was attempted. Actual Windows installation, Docker Desktop/WSL,
Linux Docker host, reverse proxy/TLS and real multi-server failover remain gates.

## Read-first Modbus register mapping (development build)

After a gateway is claimed, Add device offers Map registers before profile
assignment. Choose RTU (USB/RS-485) or TCP explicitly. Enter the target connection,
read function FC01-04, manual-listed starting address, explicit wire-address
convention and a bounded count of 1-32 registers/bits. Read sends one range request,
not a device-wide scan. No profile is needed and no register writes are issued.

The returned table preserves each wire address and raw decimal/hex word or bit.
Select the rows to include, then enter manual-backed meaning/name, stable point
ID, unit, data type, byte/word order, multiplier and accepted min/max range. The
converted preview uses decoded words multiplied by the chosen scale, with no
offset support. A 32-bit point consumes its adjacent returned word; overlapping
points, duplicate IDs, partial words and invalid/non-finite conversions block save.
Names and decoder choices are never inferred from values.

Review saves one tenant-scoped reusable profile, with all selected points and
serial framing when applicable. It selects that profile for the source form.
It does not assign a device or apply configuration. Finish source naming and
connection review, assignment, signed Apply, reload confirmation and a recent
measured reading separately. The supplied 26-row Schneider EM1X00 template remains
a separate option; its exact-model/word-order/address-convention gates still apply.

The request and reply carry an exact request ID, also fenced by tenant/gateway
when ingest stores a result. A late older reply cannot validate a newer read.
New previews require the matching updated server, ingest and edge. A prior edge
image that omits request IDs will time out explicitly rather than settle a new
preview. Older empty-ID requests can still accept their legacy empty-ID replies.
Changing connection/range invalidates the preview. No automatic retry follows an
uncertain send/save. Labels remain local to the open page until saved; unfinished
labels are not restored after reload. Check Profiles after an uncertain save.

Evidence: synthetic browser checks at 1440px and 390px, fake-serial/real-loopback TCP
raw reads, real-Postgres request fences, and a disposable broker with compiled
edge+ingest exercising the raw range before source Apply. These checks do not
prove the owner's physical meter, USB adapter, address base or decoder choice.

Installer re-run guard: running-project detection now consumes the entire Compose
ID list before testing whether it is empty. This avoids `grep -q` closing the pipe
early under `pipefail` and incorrectly treating the project's own web port as an
unrelated listener. A busy unrelated web port is still refused. The regression
uses more than a pipe buffer of fake IDs and fails against the earlier detector;
actual Docker re-run acceptance still depends on the next exact-commit CI run.
