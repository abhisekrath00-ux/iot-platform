# Service level objectives and load testing

## SLOs (p95, measured by cmd/loadtest on the production-shaped stack)

| Path | SLO | Rationale |
|---|---|---|
| Ingest: publish -> queryable | p95 <= 5 s at 100 gateways x 1 msg/s | Operators watching live dashboards see fresh data; alerts fire on current values |
| Query: series read | p95 <= 500 ms at 10 concurrent readers | Dashboards feel interactive |
| Delivery | 0 messages lost (QoS 1 + idempotent event_id) | Telemetry is the product; silent loss is the worst failure |

These are starting targets for the pilot hardware profile (single host,
local broker). Re-measure on the pilot site and adjust with the customer;
record every run on the Trello card.

## Running the load test

The harness runs inside the deployment network (MQTT + HTTP only, air-gap
safe) and needs only the JWT signing secret from `.env`:

```sh
docker compose up -d --build
go run ./server/cmd/loadtest \
  -jwt-secret "$JWT_SIGNING_SECRET" \
  -gateways 100 -rate 1 -duration 60s -query-workers 10
```

- Devices are namespaced per run (`-run-id`, defaults to a timestamp) so
  rows from older runs never skew counts.
- Ingest latency is measured on every 10th message per gateway: publish time
  to the moment the row is visible through `GET /v1/telemetry/count`.
- Query latency hammers `GET /v1/telemetry/series` on random devices.
- Exit code is non-zero on SLO breach or any message loss; CI can gate on it.

Tuning beyond the SLOs: broker and Postgres are the first bottlenecks;
see docs/deployment.md for sizing notes.
