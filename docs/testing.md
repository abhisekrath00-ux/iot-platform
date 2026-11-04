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


### OIDC callback across API replicas

`TestIntegrationOIDCCrossReplicaCallback` uses real PostgreSQL through two independent pools and a local synthetic issuer with signed RSA/JWKS tokens. It exercises start-on-A/finish-on-B, platform JWT/session persistence, a 16-callback race with one issuer exchange, replay/expiry, bad nonce, unknown user and state-store outage. Rejected identity checks cannot create sessions or auto-provision users. Run with `TEST_DATABASE_URL` and `go test ./cmd/api -run '^TestIntegrationOIDCCrossReplicaCallback$' -count=1`. This is local application evidence, not external-provider, browser or multihost failover certification.


### CI action runtime and module-cache paths (2026-11-02)

The main CI workflow uses the Node24-based `actions/checkout@v6`, `actions/setup-go@v6` and `actions/setup-node@v6`, replacing action versions whose Node20 runtime produced deprecation warnings. This changes the action runtime, not the web application's selected Node22 build version. Both Go setup steps explicitly hash `server/go.sum` and `edge/go.sum`; there is no root `go.sum` in this repository. Hosted `ubuntu-latest` runners are used. These actions require recent runner versions; self-hosted runners must meet the official requirements before adopting them. The separate edge-release workflow is unchanged and is not covered by this CI maintenance result.

Official action references: [checkout v6](https://github.com/actions/checkout/tree/v6), [setup-go v6](https://github.com/actions/setup-go/tree/v6), [setup-node v6](https://github.com/actions/setup-node/tree/v6). A green run must still be checked at the exact published commit. Updating action declarations alone does not prove warnings are gone.


### HA HTTP checkpoint tests

`python3 scripts/test-check-ha-http.py` starts only local synthetic HTTP servers. It checks exact GET paths/one request per target, readiness200, one primary200/two503, redirect/error rejection, unknown primary state, refused connection, malformed status, stalled/trickled header deadlines and input validation. Main CI runs it in compose-smoke before stack setup. This tests the probe, not a real multihost deployment.


### Generated etcd mutual-TLS contract

`bash scripts/test-install-ha.sh` covers `--etcd-mtls` alone and combined with `--shared-cache-mtls`: HTTPS client/peer/member URLs, required certificate auth/CA paths, Patroni HTTPS/client identity, private instruction-only TLS directories and service-specific mounts. CI compose-smoke parses all six node outputs. No certificate identity is generated. These are syntax/contract checks, not real etcd/Patroni transport, election or failover acceptance.

### Generated etcd mutual-TLS local-binary regression

Run `ETCD_BINARY=/trusted/path/to/etcd python3 scripts/test-ha-etcd-tls.py` with Python3/PyYAML/OpenSSL. Three disposable loopback members reuse rendered TLS flags and temporary synthetic PKI. The test checks all-member health, authenticated v3 gateway write/readback, HTTPS membership URLs, rejection of absent/untrusted client identities (client and peer), wrong trust/SAN and unavailable-certificate no-plaintext behavior. CI verifies the pinned3.5.35 fixture checksum. Vendor support for the3.5.35 patch is sourced in the HA guide; this test does not establish container mounts, Patroni TLS, multihost SAN behavior, upgrade/restore safety or failover. See [HA scope and limitations](ha-multi-node.md#local-etcd-tls-transport-regression).

The TLS fixture explicitly enables X509 strict verification and supplies CA/leaf constraints and keyUsage extensions. This reproduces and fixes the initial CI rejection of its incomplete synthetic CA, rather than bypassing the verifier. Bounded member logs are printed on fixture health failure; no production credentials are involved.

### HA synchronous-mode intent contract

Generator tests check all three nodes in both default and `--sync-strict` modes for explicit one-standby synchronous mode and `synchronous_commit: "on"`, with no hard-coded synchronous member list. This is rendered bootstrap configuration evidence, not runtime application, transaction durability, fencing or a zero-loss/failover guarantee. Existing dynamic configuration is not changed. The HA guide records cancellation/session-override and unavailable-standby limits from Patroni's vendor documentation.

### Actual Patroni DCS TLS client checkpoint

`ETCD_BINARY=/trusted/etcd PATRONI_PYTHON=/venv-with-patroni4.1.5/bin/python python3 scripts/test-ha-etcd-tls.py` adds two independent real Patroni4.1.5 DCS clients to the disposable three-member fixture. It checks HTTPS discovery, cross-client initialization/config writes/reads and fixture namespace cleanup using rendered TLS settings. CI requires it with the same Patroni pin as the image. It does not start a Patroni daemon/PostgreSQL or test leases, watchers, image/PG16 behavior, upgrade, replication or failover. The base TLS check can run without Patroni; its output must not be mistaken for this optional checkpoint. Python dependencies are not transitively hash-locked yet.

The real-Patroni checkpoint also constructs two non-patronictl DCS clients, verifies watch caches are active, checks exclusive leader-key acquisition, keepalive calls, ownership-guarded deletion and voluntary handoff, then lets a real30s lease expire and verifies member/leader disappearance and new acquisition. It waits up to45s for expiry and has a90s subprocess deadline. Watch stream restart diagnostics can occur after an occupied-key acquisition is rejected; state assertions still must pass. This is DCS-only, not a Patroni HA-loop/PostgreSQL/fencing/network-partition test or sustained lease-renewal soak.

The TLS fixture also cleanly stops its first etcd endpoint, asserts new writes/readback on both survivors, repeats the real Patroni config-only phase against the unchanged endpoint list, and restarts the stopped member with its old data to verify healthy TLS/readback of the value committed while absent. Gateway convergence15s, fresh Patroni config-only25s and member recovery20s are test deadlines, not measured SLA promises. Expected connection-refused retry diagnostics do not replace success assertions. This does not target a known raft leader, simulate a partition/crash/power loss, exercise two-member/quorum loss, prove existing watcher recovery or database/app failover.

The TLS fixture also stops two etcd processes: the remaining endpoint must still serve the old value via serializable reads before/after an unacknowledged write attempt. Only timeout or HTTP503/504 qualifies; connection refusal and unexpected errors fail. Restoring a member must allow fresh commits/readback, and restoring the last must recover that new value. Serializable reads can be stale. Timed-out writes have unknown outcomes and may commit after quorum returns; the test asserts no rollback/absence guarantee. This is clean process-stop gateway evidence, not partition simulation, Patroni failure-path handling or PostgreSQL fencing/demotion proof.

`test-patroni-quorum.py` is invoked by the TLS fixture when PATRONI_PYTHON is set. One patronictl-mode DCS object writes/reads before quorum loss, must return exactly False on its config write during loss, then uses that same object for successful new write/read and namespace cleanup after gateway-verified restoration. Parent handshakes and cleanup are bounded. Expected retry/error traceback logs accompany the induced failure; arbitrary exceptions/timeouts do not count as a pass. False remains an uncertain write outcome, not rollback proof. Existing watch-cache/lease/daemon/PostgreSQL failure paths are not tested by this helper.

The current quorum-loss phase uses SIGKILL for its two disposable etcd processes because live gRPC watches delayed graceful termination. The helper now starts a non-patronictl leader/watch client before the outage. Its repeated `update_leader` calls must eventually fail with False or the specific Etcd3Error within a 15s retry window; the last call may consume its 3s retry budget. Transient success after quorum loss is possible and observed locally; the helper counts it. Unrelated exceptions or continued success beyond the window fail. A successful keepalive is not proof of a usable write quorum. After restoration the same existing watch cache must observe a new marker written by the config client and successfully update its leader lease. Marker deadline20s and parent handshakes25s remain bounded. This adds DCS watcher/lease failure/recovery coverage, not Patroni HA-loop or PostgreSQL demotion/fencing/promotion/availability proof, and not power loss/partition/storage-corruption simulation. Prior clean-two-member wording describes the older checkpoint, not the current SIGKILL phase. Single-member stop remains graceful.


The optional real-Patroni TLS checkpoint additionally runs `test-patroni-tls-rejection.py` against the actual `PatroniEtcd3Client` transport: missing client identity, untrusted client identity and untrusted server CA must reject discovery with SSL-handshake diagnostics on all three endpoints. Fresh good-identity discovery succeeds before/after. Direct construction avoids `get_dcs`'s indefinite discovery startup retries; the25s outer timeout is a failure, not rejection evidence. The exact expected discovery exception plus endpoint SSL diagnostics are required; refusal, timeouts or arbitrary exceptions fail. Recognized SSL EOF can be a missing/untrusted-client handshake close, not a named certificate-reason diagnosis. Controlled identity changes and positive controls supply the distinction. This is transport-only, not full daemon startup, Patroni SAN/rotation/revocation coverage, PostgreSQL TLS, demotion or failover evidence. No live PKI/config changes.
