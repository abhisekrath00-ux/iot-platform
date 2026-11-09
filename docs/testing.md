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


The actual Patroni transport helper now includes a wrong-server-SAN case: reachable localhost aliases face certificates with only IP:127.0.0.1. Loopback resolution/TCP checks and explicit hostname-mismatch certificate errors on ALL three endpoints are required. SSL EOF or generic failures cannot pass this case. Positive original-address discovery before/after remains. This supersedes the prior transport-layer SAN exclusion only, not full daemon/reload/rotation/revocation/expiry or PostgreSQL/REST/API TLS gaps. No live config/PKI changes;25s timeout is failure.


The real Patroni config helper additionally verifies conditional config and `/sync` metadata revisions through two independent clients. Stale config updates return False and leave newer revision/value intact; current revision writes succeed. Sync create-if-absent, current-revision update, stale update/delete rejection, state preservation and current-revision delete/readback are required, also during the one-endpoint-down config-only rerun. A rejected sync write returns None; Patroni4.1.5's rejected conditional etcd sync delete returns an empty dictionary, not exact False. This is real DCS compare-and-swap evidence only, not synchronous database replication, concurrent races, HA-loop/fencing/promotion or quorum-loss stale-write behavior. Namespace cleanup and existing subprocess deadlines remain.


The real DCS helper now also uses two independent clients released from a thread barrier against one shared revision with distinct payloads. Three config and three sync-state races must each have exactly one winner, and both clients must read that winner's committed value/state. Sync keys are deleted at the winning revision after each round. Repeated with one advertised endpoint down. Barrier5s/result10s deadlines plus outer subprocess bounds remain failure safeguards. This supersedes the prior no-concurrent-races exclusion only for these bounded competing-writer checks, not exhaustive scheduling, stress/soak, cross-host or quorum-loss races. One metadata winner is not an exactly-once application guarantee or proof of PostgreSQL replication/HA-loop/fencing.

The healthy watch-backed phase additionally races two real Patroni clients for an empty leader key in three rounds. Exactly one nonempty successful etcd transaction dictionary and one empty dictionary are required, followed by committed-revision, owner-name and shared lease-session readback. The loser cannot reacquire or delete the winner's key; the winner renews and releases, and both watchers observe absence before repeating. This is healthy local DCS key/lease ownership coverage only, excluded from the config-only endpoint-outage rerun, not PostgreSQL election/promotion/fencing, quorum-loss races or exhaustive thread schedules.

After real lease expiry and sequential replacement acquisition, the healthy DCS helper passes the captured old-owner cluster snapshot to the former owner's public renewal path. Exact False renewal and rejected deletion are required, with both watchers retaining the replacement owner's lease session and leader-key revision. The replacement owner must still renew. This adds expired-lease stale-owner coverage for distinct member names, not same-name duplicate processes, database fencing, arbitrary races, network partitions or Patroni HA loops.

The config helper also runs three barrier-released create-only initialization-marker races using distinct synthetic system IDs. One successful transaction dictionary with revision and one empty rejection dictionary are required. Both clients read the winning ID; a third late create-only ID cannot overwrite it. Explicit fixture cancellation/readback clears the marker between rounds. Repeated with one advertised endpoint down. This is bounded local DCS marker exclusion, not PostgreSQL bootstrap, real system-ID validation, data-directory safety, replica formation, promotion, fencing, HA-loop behavior or a production reset procedure.

The config helper also verifies scheduled failover-request metadata through the real public `manual_failover` method: timezone-aware timestamp/leader/candidate readback, three competing conditional candidate updates with exactly one True/one False, stale update/clear rejection and preserved winning request, then current-revision clearing with empty fields at a new revision. An empty request is not key-deletion evidence. Repeated with one endpoint down. No HA loop consumes the synthetic timestamp or requests, so this does not test execution, candidate validation, database promotion/fencing, replication, availability or quorum-loss races.

The HA generator suite requires the exact Patroni Dockerfile PostgreSQL16-bookworm multi-platform base index digest. The November5,2026 checkpoint verified official registry index bytes plus Linux amd64 and arm64/v8 manifest/configuration digests; both configurations report PostgreSQL16.15. This is registry provenance and source-reference testing only, not an image build/run or database upgrade/replication/failover test. Apt and Python transitive inputs remain unhashlocked.

Patroni Python runtime and build-tool locks now pin versions plus published non-yanked PyPI release hashes. Dockerfile and DCS CI use hash-required installs, disable runtime build isolation after installing locked build tools and run pip check. Clean Python3.10 installation and the actual TLS/watch/lease/quorum fixture pass. Generator tests enforce lock syntax and consumer flags. Python3.11 image/arm64 execution, source-to-wheel reproducibility, security audit and full Docker build/run remain unproven; pip/Apt inputs are not pinned by these locks.

The HA generator now invokes `scripts/test-patroni-locks.py`: per-entry hash/continuation/version/duplicate checks with malformed negative cases, plus an offline synthetic-wheel pip test. Intact bytes must install; tampered bytes against the unchanged expected hash must produce a hash-mismatch error without installing the package. Temporary targets, no-index and no-cache-dir keep the test isolated. This is integrity refusal coverage, not package safety, source-build reproducibility, signatures or a full pip/platform matrix.


### Expired client identity through the real Patroni transport

The disposable TLS fixture now issues an additional client certificate with a fixed past validity window (January1-2,2000), signed by the trusted synthetic CA and using the same client key. OpenSSL verification must report expiry; separate verification with time checking disabled must accept its chain and client purpose. That disabled-time check is an isolated diagnostic only. Patroni transport verification remains enabled and unchanged.

The actual Patroni4.1.5 discovery client must reject this identity at all three endpoints with SSL-handshake diagnostics, then fresh valid-identity discovery must succeed. This passed with Python3.11.17 and etcd3.5.35 using the hash-locked Python environment. A generic connection refusal, timeout, unrelated exception or accepted discovery fails the test. As with the existing missing/untrusted-client cases, TLS EOF can be a server-side handshake rejection; this test does not claim every runtime produces an explicit expiry alert. Controlled identity, independent expiry/chain checks and valid-identity controls bound the evidence.

No production certificates, trust settings or deployments change. This covers expired-client discovery rejection only, not certificate rotation/reload, revocation, server-certificate expiry, PostgreSQL TLS or the Patroni HA loop. Full Docker/PG16-image and arm64 execution remain unproven. The Python3.11 result is standalone fixture evidence, not image execution.


### Client certificate purpose rejection

The disposable transport fixture also issues a trusted, currently valid certificate with serverAuth only, never clientAuth. OpenSSL verification must accept its chain for sslserver and reject sslclient with a certificate-purpose diagnostic. Actual Patroni discovery using that certificate/key must then fail TLS on all three healthy endpoints. Valid client discovery still succeeds before and after the negative cases. This passed with Patroni4.1.5, Python3.11.17 and etcd3.5.35.

This isolates extended-key-usage rejection from missing identity, untrusted CA and expiry. It does not establish server-side subject/RBAC authorization, revocation, rotation/reload, full daemon startup or database HA. TLS EOF remains a permitted handshake diagnostic under the controlled-identity checks; the test does not require a named purpose alert from every runtime. No production trust material or settings change; Docker/PG16-image and arm64 execution remain unproven.


### Watch-backed member metadata fixture

Two independent runtime DCS clients now check member heartbeat metadata. After both observe fixture-a and fixture-b, an unchanged fixture-a heartbeat must preserve its member object, key revision and lease session. Updating synthetic tags/metadata must become visible to both watch-backed observers with a new key revision and the same lease session. Restoring the original metadata must advance the revision again and retain that session. Fixture-b's member object must remain unchanged throughout.

This passed with Patroni4.1.5, Python3.11.17 and the disposable etcd3.5.35 TLS cluster. It verifies bounded member-key/cache behavior, not database state, member liveness, collision handling for duplicate names, tag effects in the HA loop or production discovery. Existing lease-expiry, leader ownership, TLS and quorum checks still run afterward. No PostgreSQL daemon or production state is involved.


### Pinned Patroni image build/runtime checkpoint

The Compose-smoke CI job now attempts a Docker build of the pinned `deploy/ha/patroni` recipe and a network-disabled runtime smoke check. It requires the image's declared user and actual process user to be non-root `postgres`, PostgreSQL major16, Python3.11, Patroni4.1.5, importable etcd3/psycopg2 modules, `pip check` success and an executable entrypoint. The step has a12-minute deadline and uses only a local runner image tag, with no registry push.

This checkpoint is not a claim that an uninspected run passed. Read the exact commit's CI job before using its result. Docker is unavailable in the local test environment; local YAML/shell validation does not prove the image builds. A successful checkpoint would cover the runner's native architecture only, not arm64, entrypoint startup, configuration/volume permissions, PostgreSQL initialization/replication, Patroni's HA loop, promotion/demotion, fencing or failover. The runtime command overrides the entrypoint and starts no database daemon. No production deployment or trust material changes.


### PostgreSQL16 disposable volume restart checkpoint

The Compose-smoke CI job now attempts `bash scripts/test-ha-postgres-image.sh LOCAL_IMAGE` using the pinned image built in the preceding step. Two sequential network-disabled containers override the entrypoint and run as the image's non-root postgres user against one disposable named volume. The first initializes PostgreSQL16, starts it with TCP listening disabled, writes and reads a synthetic row over a Unix socket, then cleanly stops. The second must open that existing data directory without initializing it, start PostgreSQL again and read the exact row and row count back. Both require major16 data and a writable PGDATA volume. Containers and the volume are removed at exit; a volume cleanup failure fails the check. CI bounds the step to three minutes.

Read the exact commit's step log before claiming success. Docker is unavailable locally, so shell syntax and a fake-Docker orchestration test do not prove database behavior. A successful CI result covers native-runner PostgreSQL initialization, named-volume ownership and clean-restart persistence in this synthetic fixture only. It does not cover abrupt termination, durability under power loss, production bind mounts, upgrades, replication, PostgreSQL TLS, arm64, actual Patroni entrypoint/configuration/HA-loop startup, promotion, fencing or failover. Trust authentication is confined to this network-disabled disposable fixture and is not a deployment setting. No production volume or registry is used.


### Independent member-watch readiness

CI1498 failed before the image/database steps: the member metadata fixture waited for observer-a to see both members, then immediately read observer-b without waiting for its independent watch cache. The baseline capture now waits for both observers separately, within the existing convergence deadline. Missing member/session/revision and later metadata/lease invariants still fail; no runtime assertion or TLS check is removed. This fixes test readiness, not a production cache-consistency guarantee. CI1498 does not prove the skipped PostgreSQL volume checkpoint.


### PostgreSQL16 immediate-stop recovery checkpoint

The disposable image fixture now keeps the existing initialize/clean-reopen checks and adds two more network-disabled non-root containers against the same named volume. The crash phase verifies fsync, synchronous_commit and full_page_writes are on, commits a second synthetic row, and calls `pg_ctl -m immediate`. It requires pg_controldata to show the unclean "in production" state. The recovery phase must reopen without initialization, read both committed rows and exact row count, and show PostgreSQL interruption and redo-start diagnostics before a clean stop. All four phases remain inside the three-minute CI deadline; container and volume cleanup still run.

This is a checkpoint definition, not a claim that an uninspected CI run passed. Read the exact tip's step log for all four phase markers and recovery diagnostics. Local fake-Docker/shell tests are only orchestration checks. Even a passing run covers an immediate PostgreSQL server stop with the runner and storage still alive, not host/container kill, power loss, storage faults, corruption, production bind mounts, sustained workload, upgrades, replication, PostgreSQL TLS, Patroni startup/HA-loop/fencing/failover or arm64. The entrypoint remains overridden, and trust authentication remains confined to the disposable network-disabled fixture. No production state or registry changes.


### Uncommitted transaction recovery checkpoint

The immediate-stop phase now starts a separate psql connection, begins a transaction, inserts synthetic row3 and emits a readiness marker only after INSERT. It then sleeps inside that transaction, with no COMMIT. A bounded ten-second readiness wait requires the client process to stay alive, and another connection must not see row3. The immediate stop must break that transaction connection with a server-termination diagnostic rather than accepting a clean completion. Recovery must retain committed rows1/2 and exact count2 while explicitly excluding row3.

Read the exact CI step before treating this as passed. Shell/fake-Docker checks do not execute PostgreSQL. A passing run would establish this synthetic open-transaction exclusion after an immediate server stop only, not power-loss durability, every isolation level, concurrent load, host/container kill, storage faults, replication, Patroni HA or failover. The existing network-disabled/non-root/disposable-volume boundaries and three-minute CI deadline remain. No production transaction, data or authentication setting changes.


### Single-node actual Patroni entrypoint checkpoint

CI now attempts `scripts/test-patroni-image-bootstrap.sh` after building the pinned image. A checksum-verified etcd3.5.35 binary runs in a separate non-root container on an internal Docker network. No ports are published. The Patroni container uses the shipped entrypoint, a read-only synthetic configuration and a disposable named volume. The fixture requires its local /primary response to report running primary, PostgreSQL16 SQL to report not-in-recovery, and a synthetic committed row. After stopping/removing that container, a replacement using the same data volume and still-running etcd must become primary again and retain the PostgreSQL system identifier plus exact row/count. CI allows five minutes and cleanup removes containers, network and volume.

Read the exact commit's actual step log before claiming this passed; local syntax/fake-Docker tests do not execute the daemon. The synthetic configuration intentionally has one member and synchronous mode off. It is NOT the generated three-node production configuration and must never replace that policy. Etcd uses plaintext only on the isolated fixture network; this does not change production TLS settings. Local SQL trust is synthetic fixture-only. Even success covers single-node entrypoint/bootstrap/restart only, not replication, multi-node leader changes, synchronous guarantees, fencing, failover, network partitions, PostgreSQL TLS, production bind mounts, upgrades or arm64. No production services, data, credentials or registry are used.

### Actual asynchronous Patroni replica checkpoint

The entrypoint fixture now also attempts to join a second Patroni/PostgreSQL16 container using a separate empty named volume, the same isolated etcd and synthetic credentials, and the shipped entrypoint. It requires a running replica REST response, SQL recovery/read-only state, the same system identifier and the pre-existing row. Only after replica readiness, the primary commits a second row. The standby must replay at least the captured primary WAL LSN and read the exact second row/count. An INSERT on the standby must fail specifically as a read-only transaction; both members must still contain exactly two rows. The replica is marked nofailover and stopped before the primary; both volumes and the internal network are removed. The existing five-minute CI deadline applies.

Read the exact tip's actual step log before treating this as runtime evidence. Syntax and fake-Docker tests are orchestration checks only. Even a passing step proves only synthetic asynchronous two-member clone/replay and write rejection, not synchronous durability, failover/fencing, DCS quorum tolerance, partitions, production TLS or mounts, upgrades, arm64, or the generated three-node production policy. No ports are published and no production service or credential is used.

### Actual asynchronous replica replacement/catch-up checkpoint

After the two-member clone/replay/write-rejection assertions, the fixture now attempts to stop and remove only the standby container, keeping its named volume and leaving the primary online. The primary commits a third row while the standby is absent. A replacement standby uses the same volume, configuration and shipped entrypoint, must retain the cluster system identifier and recovery/read-only state, replay through the captured primary WAL target and read all three exact values/count. The primary must retain its system identifier, count3 and not-in-recovery state. Cleanup stops standby before primary and removes both data volumes. The five-minute CI step deadline remains.

This is a short synthetic asynchronous outage/replacement drill, not a retained-WAL-duration guarantee, resilience to long outages, slot-bloat bound, failover, fencing, synchronous durability, partitions, DCS quorum tolerance, or production three-node acceptance. Inspect the exact tip's actual CI step log before claiming runtime success; fake-Docker checks cannot prove PostgreSQL or Patroni behavior. No production data, credentials, ports, service, registry push or deployment is involved.

### Actual short single-DCS restart checkpoint

The same synthetic entrypoint fixture now also attempts to restart its existing etcd container, retaining that container's data directory. It requires a fresh successful etcd health response, primary and replica readiness, unchanged PostgreSQL system identifiers and expected recovery roles. Only afterward the primary commits a fourth row. The standby must replay through the captured WAL target and return the exact fourth value; both members must contain count4. The five-minute CI deadline and full disposable-container/volume/network cleanup remain.

This is a short same-container restart of one plaintext fixture DCS member, not a quorum-loss, prolonged-outage, data-loss, fail-closed demotion, fencing or leader-election drill. It does not establish durability across DCS container replacement or validate production etcd TLS/mounts/backup, synchronous PostgreSQL guarantees, production three-node policy, upgrades or arm64. Read the exact tip's full runtime log and all CI jobs before claiming success; fake-Docker tests are orchestration checks only. No production service, credential, data, port or deployment changes.

### Actual sole-DCS outage demotion/recovery checkpoint

After the short restart checks, the synthetic fixture now attempts to stop its sole etcd container and verify it is stopped. With failsafe off and the standby marked nofailover, the former primary must enter SQL recovery/read-only state within a bounded wait. Its live REST /primary endpoint must return HTTP503, and an actual INSERT must fail specifically as a read-only transaction, not from a connection error. Both databases must retain count4. The same etcd container/data is then started; original primary and replica readiness and identifiers must recover, and a fifth primary row must replay through a captured WAL target with exact value/count5. Full cleanup and the five-minute CI limit remain.

Patroni offline demotion stops PostgreSQL then follows as a replica, so this check requires database read-only behavior, not an indefinitely stopped database. It does not measure a maximum write exposure window, prove multi-member DCS quorum/partition tolerance, hardware fencing, automatic standby promotion, synchronous durability, lost-DCS-data recovery, or production three-node/TLS/mounts/upgrades/arm64 readiness. The short outage uses synthetic local data and credentials with no published ports. Runtime success requires reading the exact tip's complete actual step log and all four CI jobs; fake-Docker checks only test orchestration.

### Repeated stopped-DCS read-only checkpoint

The sole-DCS outage phase now requires three independent rounds with two three-second waits between them. Each round verifies etcd is still stopped, both former primary and standby report SQL recovery/read-only, live former-primary /primary returns HTTP503, and actual INSERTs on both members fail specifically as read-only transactions while counts remain4. After restoring DCS and replaying the fifth committed row, both databases must return the exact ordered list of all five expected values as well as count5.

This strengthens a single observation into repeated checks over a short fixture interval. It is not continuous monitoring between samples, a measured maximum write-exposure window, hardware fencing, a long outage/partition or multi-member-quorum test. The existing asynchronous/no-failover, synthetic-credential, internal-network and production-readiness exclusions still apply. Read the exact tip's full runtime step log before claiming these rounds passed; fake-Docker tests cannot establish runtime behavior.

### Embedded Python syntax regression gate

Before image builds, Compose-smoke now runs `python3 scripts/test-patroni-embedded-python.py`. It extracts the four literal `/opt/patroni/bin/python -c` arguments from the shell fixture with shlex and compiles them without executing them. Five tests check current syntax, rejection of the CI1513-style indented top-level try, rejection of another syntax error, a missing-invocation review gate and compile-only behavior. Changes to the number or form of invocations require reviewing this deliberately narrow gate.

This closes the specific shell-syntax/fake-Docker blind spot that let CI1513 reach runtime with invalid embedded Python. It is not a complete Bash parser, expansion evaluator, semantic Python test, dependency import check or substitute for the actual Patroni runtime fixture and exact-tip all-job verification. It does not extend the accepted HA evidence.

### Cross-member TCP password regression

The synthetic Patroni fixture now runs a SQL client in the opposite member and connects by the internal network alias to each PostgreSQL member, both after replica join and after sole-DCS restoration. The configured synthetic superuser password must authenticate, and the query must report the postgres role with a non-null TCP client address. An intentionally wrong password must fail specifically with password authentication failure; an absent password must fail specifically with no password supplied. Clients use no-prompt mode, a three-second connection timeout and a nonexistent password-file path. Connection failures are not accepted as authentication refusal.

This checks actual fixture TCP password enforcement instead of local socket trust. It does not prove production credential strength/rotation, application or replication-role privileges, TLS/encryption, network isolation outside the fixture or authorization of other services. The synthetic local socket trust/internal plaintext DCS/no-published-ports restrictions and all earlier HA exclusions remain. Runtime acceptance still requires the exact tip all-four-green CI and complete actual step log, including both TCP checkpoints.

### Stored SCRAM and replication-role flag checkpoint

Each cross-member TCP authentication checkpoint now also queries boolean results from PostgreSQL catalogs on both members, after join and after DCS restoration. Exactly the two configured users must have login enabled and a non-null SCRAM-SHA-256 password verifier. The replication user must exist once, with login/replication enabled and superuser, create-role, create-database and bypass-RLS flags disabled. Queries never select or print password verifiers themselves; missing rows and null passwords must fail the checks.

These are observed fixture credential-format and role-flag checks, not proof of all effective privileges, object grants, inherited roles, database access, production password strength/rotation or application authorization. They add no TLS, production deployment or wider HA claim. Runtime acceptance requires the exact tip all-four-green CI and full actual step log, not local mocks or SQL text alone.

### Replication-user checkpoint SELECT refusal

Both cross-member TCP checkpoints now first authenticate as the fixture replication user and require its own role plus a non-null TCP client address. That authenticated user must then fail an actual SELECT on the superuser-owned public.patroni_image_checkpoint table specifically with table permission denied, on both members after join and after DCS restoration. Connection/authentication failures and other SQL errors are not accepted as permission refusal. The existing correct/wrong/absent superuser password, stored SCRAM, role flag and recovery checks remain.

This proves one actual table-read boundary in the synthetic fixture, not all effective privileges, schema/object grants, role memberships, replication-protocol scope, production authorization or credential readiness. It neither changes production grants nor adds a wider HA or TLS claim. Exact-tip all-job CI and full runtime logs are still required for acceptance.

### Abrupt primary-container recovery checkpoint

After sole-DCS recovery and verification of five committed values on both members, the synthetic fixture now sends SIGKILL to the original primary container. It requires stopped state and exit code137, removes only that container, and starts the shipped entrypoint on the same primary volume. The original primary and nofailover standby must recover their original roles and system identifier, retaining all five exact values. A sixth post-restart primary write must replay through a captured WAL target, with all six exact values/counts on both members. Both cross-member TCP credential, SCRAM, role-flag and replication-user SELECT refusal checks run again before cleanup.

This is abrupt process/container loss with the original persistent volume and DCS data retained. It is not host power loss, disk corruption/loss, storage durability under hardware failure, automatic standby promotion, a measured recovery-time objective or zero-loss failover. The standby remains asynchronous and nofailover. All existing synthetic credential, internal-network, TLS and production-readiness exclusions remain. Acceptance requires exact-tip all-job CI and the complete actual runtime log including the SIGKILL checkpoint.

### Abrupt standby-container recovery checkpoint

After the primary SIGKILL recovery checkpoint, the fixture sends SIGKILL to the standby, requires stopped state and exit137, and removes only that container. The original primary must remain writable with its original system identifier and accept a seventh row while the standby is absent. The shipped entrypoint starts the standby on its own existing volume. It must return with its original system identifier in recovery/read-only mode and replay through the captured WAL target. Both members must have the exact seven values/counts. An actual standby INSERT must fail specifically as a read-only transaction, leaving both counts unchanged. Both cross-member TCP credential, SCRAM, role-flag and replication-user SELECT refusal checks run again before cleanup.

This is abrupt standby process/container loss with its persistent volume retained, not lost/corrupt storage, host power failure, promotion, synchronous durability, a recovery-time objective or production failover acceptance. The original primary and DCS remain online during this phase. Existing asynchronous/nofailover, synthetic credential, internal-network and production-readiness exclusions remain. Acceptance requires exact-tip all-job CI and the complete actual runtime log including the seventh-row replay and write-refusal checkpoint.

### Both-database-container abrupt restart checkpoint

After individual primary and standby SIGKILL recovery, the fixture sends SIGKILL to both database containers in one command and requires both stopped with exit137 before either is replaced. The sole DCS must remain running. Only the two containers are removed; their separate original volumes and DCS data stay intact. The original primary is started first and must become ready before the standby starts. Both original system identifiers/roles and all seven exact values/counts must survive. A new eighth primary row must replay through its captured WAL target, with exact eight values/counts on both members. Standby INSERT must fail specifically read-only without changing counts, and both-member TCP credential/SCRAM/role/replicator SELECT-refusal checks repeat before cleanup.

This is a two-database-container process restart drill with an explicitly ordered recovery and DCS/volumes retained. It is not host power loss, storage corruption/loss, full-cluster/DCS loss, automatic promotion, unordered recovery, a measured RTO or synchronous/zero-loss failover. Existing asynchronous/nofailover and production/TLS/security exclusions remain. Exact-tip all-job CI and the complete actual runtime log including the eighth-row checkpoint are required before acceptance.
