# Testing

## How to run

```bash
# Go unit tests
cd server && go test ./...
cd edge && go test ./...

# Web
cd web && npm test

# End-to-end lab smoke: compose up, seed demo, publish a synthetic reading
docker compose up -d --build
docker compose exec api /bin/api --seed-demo
mosquitto_pub -h localhost -t 't/demo/g/demo-gw/telemetry' \
  -m '{"event_id":"test-1","device_id":"meter-1","point_id":"kwh","value":12.5,"unit":"kWh","observed_at":"2026-09-27T10:00:00Z","schema_version":1}'
curl localhost:8000/v1/telemetry/latest?device_id=meter-1
```

## Test matrix (pilot gates)

| Area | Parameters | Pilot gate |
|---|---|---|
| Serial & sensor | baud/parity, register map, CRC, timeout/retry, cable length, bus contention, debounce, counter rollover, removal, power cycling | every supported device profile documented; zero unexplained readings in golden-vector tests |
| Data quality | units/scaling, monotonic counters, impossible values, clock skew, duplicate/late/out-of-order events | quality flags visible in API + UI; no double counting on replay |
| Offline resilience | 24-72h WAN loss, full disk, broker restart, brownout, reconnect storms | 72h buffer at measured sample rate; replay inside recovery window; drops alarm explicitly |
| Performance | 1/10/100 gateway cohorts, msg/s, p50/p95/p99 ingest lag, dashboard query time, storage growth | targets set after pilot traffic estimate (proposal: p95 <5s fresh telemetry, p95 <2s common queries) |
| Security | expired/revoked cert, cross-tenant access, replay, secret extraction, signed-update rejection, flow escalation, MCP injection/SSRF | no unresolved critical/high on release; independent pen test before enterprise GA |
| Recovery & UX | backup restore, bad migration, failed OTA, installer time, alert comprehension, keyboard/mobile | restore drill meets RPO/RTO; 5 installers complete setup unaided after fixes |

## Conventions

- Golden serial frames per device profile live in `edge/internal/driver/testdata`.
- Replay tests must prove dedupe by `event_id`.
- Every bug fix lands with a failing test first.
- Test electrical and metering accuracy against the meter's certified specs;
  software tests never establish measurement accuracy.
