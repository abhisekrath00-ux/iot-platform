# Testing

## How to run

```bash
# Go unit tests
cd server && go test ./...
cd edge && go test ./...

# API integration tests need a Postgres (skipped when TEST_DATABASE_URL is unset; CI runs them)
TEST_DATABASE_URL='postgres://postgres:test@localhost:5432/hexmon_test?sslmode=disable' \
  go -C server test ./cmd/api
# Edge packages for linux/windows x amd64/arm64
scripts/build-edge.sh v0.2.0    # output in dist/edge

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

## Disposable MQTT transport acceptance

`TestIntegrationMQTTTLSPipeline` requires a disposable `TEST_DATABASE_URL`,
`TEST_MOSQUITTO_BINARY`, and `TEST_INGEST_BINARY` built from `server/cmd/ingest`.
It starts a loopback certificate-required broker with ephemeral test certificates.
Its ACL is produced by `brokeracl.Generate` and written with `WriteAtomic`, the
same generator and file writer used by broker-file regeneration.

The API certificate identity publishes diagnostic and scan requests. The gateway
certificate receives its own requests but not requests for another tenant or
another gateway. An allowed scan from the same publisher follows the foreign
requests as an ordering barrier. The compiled ingest path then checks two TLS
reconnects, gateway-scoped telemetry/scan results/command ACKs, payload tenant
spoof rejection, duplicate telemetry handling, Postgres persistence and JWT-scoped
HTTP reads. Foreign telemetry is checked after an allowed same-client barrier.
These are synthetic messages on a disposable broker, never actuator requests.

This acceptance does not prove deployment certificate provisioning, certificate
revocation, broker reload, atomic replacement of multiple files, real equipment,
or lossless delivery during a disconnected clean session. CI runs this test
separately so a missing test binary cannot be mistaken for a transport pass.

## Large point catalogs

`GET /v1/points` keeps its legacy all-or-error array response, with a 5000-row
limit. Opt in to keyset pages with `?page_size=1000` (1-1000) and pass the returned
`next_cursor` as URL-encoded `after` on the next request. The response is
`{"items":[...],"next_cursor":"..."}`; absent `next_cursor` means complete.
Pages sort by device ID then point ID using C collation, not display name. Each
page is tenant-filtered and `no-store`; customer-scoped users remain refused by
the existing route allowlist. Cursors are positions, not credentials or signed
proof, and cannot grant access to another tenant.

This is not a snapshot across requests. Configuration edits during traversal
can add/remove rows or change labels; refresh after edits. Reports and map-source
pickers buffer pages and reject failed/malformed/duplicate/cycling responses
instead of showing a successful partial catalog. They cap work at 100 pages
(up to 100000 points); overflow is an explicit error, not silent truncation.
Large catalogs still use browser-side filtering and rendering after traversal,
not a server-side search UI; the row cap is a safety bound, not a performance
promise. Native installs, full Docker/HA and real multihost tests
remain separate acceptance work.

## Report parameter option completeness

Site/asset/device download options remain bounded to5000 tenant devices. The
server now queries one extra row and returns422/no-store on overflow rather
than returning a successful partial option set. Parameterized downloads using
site/asset selection use the same guard. Database failures (including site or
asset label reads, Scan and iteration errors) return generic500, not empty
successful labels or raw SQL errors. Empty successful device lists are arrays.
These reads are current views, not a transactionally consistent configuration
snapshot; refresh after configuration edits.

Reports clears stale selector data while refreshing, disables site/asset/device
changes on loading/failure, validates the whole response, and offers retry.
Existing overrides and drafts remain unchanged; downloads still use the existing
server validation and can fail if configuration changes. This is fail-closed
completeness, not parameter-option pagination, server search or report parity.

### Embedded report summary integrity

Embedded summaries distinguish a successful empty telemetry read from an unavailable read. A failed aggregation produces one unavailable note, never a partial summary or "no readings" claim. Missing/cross-tenant report IDs remain unavailable without exposing another tenant's name. Database lookup failures leave availability unknown; invalid saved definitions produce an invalid-definition note. These are explicit notes inside an otherwise usable parent report, not a guarantee that every embedded report succeeded.

`TestIntegrationSubreportUnavailableNotEmpty` exercises a second-metric failure after a successful first read, an empty result, valid summary math, tenant isolation, an invalid definition, and a closed database pool.


### HA response-cache mode

`TestResponseCacheModes` checks explicit off/local/shared modes, missing shared endpoint and invalid values. `TestIntegrationOffCacheReplicasReadDatabaseWrites` checks two API handlers over real PostgreSQL see subsequent database writes without response-cache hits and do not serve old success after database outage. `scripts/test-install-ha.sh` checks both render modes omit the unused independent Redis service/volume/password, keep PostgreSQL OAuth state, and quote the cache-off YAML string. These checks do not prove multihost capacity or failover.
