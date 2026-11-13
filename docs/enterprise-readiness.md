# Enterprise readiness: evidence and open gates

Review date: 13 November 2026. **Not enterprise-ready or ready for owner update.**
The owner wants the complete server-to-edge product, not an isolated recovery
fixture. The handoff must include server update steps and a fresh edge EXE
available from the dashboard, with matching version, checksum and installation
instructions. Existing installation, identity and saved configuration stay intact.

This is a code/evidence review, not approval to change an owner deployment, add
infrastructure, spend money, publish a release or merge main. Work stays on free
local/disposable test resources. Do not assume multiple nodes or a new broker,
cache service or queue just because the requirement says "enterprise".

## Architecture already present

The existing modular Go API, ingest process, PostgreSQL, MQTT broker and edge
SQLite queue remain the starting point. Cache and background-work changes must
fit their actual consistency, authorization and failure boundaries.

| Area | Grounded implementation | Evidence and remaining gate |
|---|---|---|
| Read caching | `cmd/api/cache.go` gives live reads 2s TTL, configuration lists 10-30s, tenant invalidation after successful mutations. Customer-scoped users bypass shared tenant entries. `internal/respcache` bounds entries/body size, coalesces concurrent misses and refuses personalized/cache-control responses. | Local/shared unit and race tests; real-Postgres API tenant/invalidation test. A bounded JWT+HTTP+PG baseline now mixes300 reads,20 profile writes and20 immediate read-after-write checks across2tenants/4readers; cache hits and invalidation observed. Small synthetic fixture only; mixed MQTT ingest/report load and outage/soak still unproved. |
| Optional shared cache | `internal/redisx` supports verified TLS, optional client certs, ACL credentials and DB selection. Shared read errors bypass caching, not stale local fallback. Generation invalidation spans replicas when the store succeeds. | Fake transport/failure tests remain distinct from the real `TEST_VALKEY_BINARY` test, now executed against hash-checked official Valkey9.0.7 with race detector. Two in-process caches share one actual mTLS/ACL endpoint; no real multi-host/failover evidence. Failed invalidation permits TTL-bounded stale entries; no stronger guarantee. No distributed miss lock. |
| Cache handler failure | A handler panic formerly retained its single-flight entry indefinitely. Local correction releases only the failed flight, keeps partial output uncached and preserves the panic for HTTP handling. | Red regression reproduced for both modes, then cache race suite10repeats and real loopback HTTP recovery test pass. Cache race10repeats, real loopback HTTP panic/retry, and full fresh-Postgres Go suite pass. Local tested; separate exact5ee8d8d CI passes all4jobs, not a deployed fix. |
| Login state | `cmd/api/sessions.go` stores tracked JWT session IDs/revocation in PostgreSQL. Configured OAuth one-use state is a separate store; readiness depends on it when shared state is selected. | Do not describe all sessions as Redis-backed or use cache outage policy for authentication. Tenant/role/customer scope and revocation must remain checked before cached handlers. |
| Telemetry ingest | `cmd/ingest/main.go` validates topic-bound identity, inserts deduplicated telemetry, then invokes position/rules/flows synchronously. Duplicate inserts skip derived work. Reconnect reinstalls all subscriptions and refuses silent partial subscription. | Fresh local baseline on this review: compiled edge pairing/ACL watcher, certificate-required MQTT/compiled-ingest reconnect and source-apply/live-reload/110-reading tests all pass on fresh PG. These are synthetic source tests, not hardware or load evidence. Need sustained ingest with dashboard/report reads, DB interruption, restart/reconnect and post-insert derived-work failure tests. No durable derived-work/outbox guarantee follows from QoS1. |
| Background work | API starts leader-elected report, flow, KPI, retention/index jobs using PostgreSQL advisory-lock ownership. Report concurrency has refusal limits rather than an unbounded queue. | Lock tests and component tests do not prove all jobs together under load. Need ownership loss, shutdown, bounded backlog, duplicate effect and restart evidence. Preserve explicit notification/control approval gates. |
| DB performance | Per-replica pool is configurable; telemetry has partitions/retention/rollups. Report load notes include a 2.4M synthetic row test and significant dead-row/vacuum sensitivity. | No customer capacity figure inferred. Capture query plans, pool waits, row growth, p95 and memory on a defined local fixture; test tenant fairness and overload refusal. |
| Observability | Health/readiness, logs and load harness exist. Admin snapshot/Prometheus/support and minute history now expose cache hit/miss/invalidation/failure counters and API client-pool utilization/acquisition stats. | Real-PG integration and race tests cover exports, disabled cache, history and admin boundary. No cluster aggregation, automatic alert thresholds or operator backlog monitoring is inferred; built-in charts do not yet graph these new signals. |
| Installation/update | Production provider now owns staging/proofs/cleanup with exact journal/image/manifest guards. Native Windows and Linux fixture evidence is recorded in `edge-secure-migration.md`. | v63 exact8f0354c scoped native fixture passes all3jobs/Linux44m26s, including lost-both-cleanup recovery; v62 interpreter crash remains unaccepted/unexplained. Later cache commits are not covered by that native run. Production entry/reviewer/menu integration is incomplete. |
| Backup/restore | Migration recovery protects the exact reviewed DB/runtime transaction and retained rows. Existing backup/restore tools are separately documented. | Actual backup/restore scripts now roundtrip a full93-table synthetic schema into a second fresh local PG DB via Docker transport shim; all row fingerprints/migration reapply/tenant latest-read boundary pass under race detector. Still not clean-host/container/edge key/files/secrets/Windows/real-certificate recovery or a measured RPO/RTO. |
| Dashboard fresh edge package | `EdgeDownloads.tsx`, `cmd/api/edge_packages.go`, package build manifest and native installer tests implement same-server authenticated inventory/download plus SHA256 verification. | Need final combined build/version, actual dashboard download and fresh Windows install/pair/source/test/save/apply/recent-reading handoff. UI/download tests with fake EXE bytes do not prove the executable. |
| Hardware/deployment | Protocol drivers and source setup exist, with model-specific profile gates. | Physical COM port/slave/submodel/word decoder and actual meter readings remain unverified. Deployment TLS/ACL, target load/soak and external review gaps remain explicit. |

## Next acceptance sequence

1. Finish exact v63 native diagnostics and retain all failures. Do not skip fresh
   guards or treat a changed timer as a proven crash correction.
2. Finish production reviewed entry/menu and combined dashboard package flow.
   Prove no owner system is modified during preparation and no defaults silently
   create discovery, anonymous network access or managed commands.
3. Run a bounded local mixed-workload baseline with defined tenant/device/rate,
   read/report clients, duration and resource limits. Capture freshness, query
   latency, errors/refusals, pool use, CPU/memory, queue/backlog and duplicates.
4. Inject cache outage, handler failure, DB unavailability, broker disconnect and
   one process restart, separately and in the mixed flow. Require recovery and
   tenant isolation, identify any durability gap before selecting a queue change.
5. Verify backup/restore and final server update plus dashboard EXE together.
   Report remaining owner/hardware/deployment checks without calling the product
   enterprise-ready. Any live deployment, external effect or paid resource still
   needs its existing authority.

## Source pointers and limits

Read code and exact tests, not an old "implemented" label, when changing a gate:

- `server/cmd/api/cache.go`, `server/internal/respcache/*`
- `server/internal/redisx/{config,redisx,preflight,valkey_test}.go`
- `server/cmd/api/{main,sessions,cache_test,cache_mixed_test,edge_packages,edge_packages_test}.go`
- `server/cmd/ingest/main.go`, `server/internal/leader`, `edge/internal/queue`
- `web/src/components/{EdgeDownloads,EdgeDownloads.test}.tsx`
- `docs/{edge-secure-migration,backup-restore,report-load-test,slo,deployment}.md`

This review corrects stale architecture prose about Redis sessions, TLS and
outage behavior. Other historical readiness docs may still carry earlier scope
and must not be read as current approval or acceptance.

## Local baseline records (13 November 2026)

- Cache race suite10repeats passes, including actual loopback HTTP recovery after
  injected cached-handler panic in both local/shared modes. Shared transport in
  this contract is a deterministic fake, not a real Redis deployment.
- `TestIntegrationMixedCacheReadsAndProfileWrites` with race detector:2tenants,
  4readers,300reads across profiles/latest/series,20profile writes,20postwrite
  cache-freshness checks.340completed requests, zero test failures. Example run
  elapsed192ms,p50 1.84ms,p95 7.70ms; tiny seeded local data, not a capacity number
  or SLO pass for a real deployment. JWT signature/tenant boundary is exercised;
  full live-role/session-revocation middleware is outside this harness.
- Real compiled edge/ingest with disposable Mosquitto2.0.11 and fresh local
  PostgreSQL: pairing proof+ACL watcher, mTLS MQTT reconnect/identity/dedupe and
  source apply+live polling reload all3pass. Synthetic Modbus TCP110reading,
  no physical meter/Windows SCM/production endpoint claim. Separate tests, not
  one combined load/fault experiment.
- Full fresh-Postgres Go suite passed after the cache panic fix. New mixed-flow
  test is individually race-checked and a final full fresh-Postgres suite refresh
  including it also passes; separate remote Go/web/security/Compose CI now passes exact5ee8d8d. Native v63 is a different exact snapshot and passes scoped lifecycle acceptance, not these later cache changes.

- Official Valkey9.0.7 Jammy x86_64 binary, SHA256
  `bf0f19982dd464ea111617608a90f1a6eaad2c9c59b2e289cbc1591129f6d2e4`:
  `TestValkeyTransport` under race detector passes3repeats. Actual verified TLS,
  client certificate enforcement, hostname/CA/password/ACL refusal, selected DB
  isolation, command preflight, one-use GETDEL and TTL are exercised. Two
  in-process cache instances share that endpoint;5generations each launch12reads
  after completed invalidation and assert fresh values. An injected handler panic
  leaves no poisoned flight or partial shared entry; a retry and other-instance
  hit succeed. Killing the endpoint bypasses caching rather than returning stale
  local data. No overlapping-write linearizability, external deployment, multi-host
  failover or capacity claim. Noble binary requires a newer glibc than this host;
  the compatible official Jammy binary was used, with no OS changes.
  Vendor sources: https://valkey.io/download/ and
  https://download.valkey.io/releases/valkey-9.0.7-jammy-x86_64.tar.gz with
  https://download.valkey.io/releases/valkey-9.0.7-jammy-x86_64.tar.gz.sha256.

- `TestIntegrationMQTTTLSPipeline` now includes a single combined bounded flow:
  actual mTLS Mosquitto/generated ACL -> freshly compiled ingest -> fresh PG ->
  cached HTTP latest and uncached series with JWT tenant separation.80synthetic
  events plus8duplicate publications run alongside4readers x150requests; one
  extra ingest TCP reconnect occurs while reads continue. Publishing pauses for
  subscription recovery, so no disconnected clean-session losslessness is implied.
  All80unique events remain once and the final5079reading becomes visible through
  the2s cache TTL. Example race run:4.02s mixed section,612HTTP requests including
  freshness polling,maxHTTP11.46ms,310cache hits,client pool2/20 and2empty
  acquisitions. These are local synthetic observations, not capacity/SLO numbers.
  No report execution, full auth/session middleware, all background jobs, DB fault,
  broker process restart, hardware or production deployment is covered. Initial
  harness run incorrectly timestamped the new batch before its own previous
  future-stamped barrier; rows were stored but the older latest value correctly
  won. New batch is now explicitly after the retained max timestamp; fresh rerun
  passes. This was a fixture correction, not a product fix.

### Separate remote cache snapshot result

Run43386173504 on d0aaafc is **not accepted**: web/security/Compose pass but
Go stops in `TestActualPostgresConnectedDeniedClosedAndNoMutation`. The fixture
passed CI's DNS name `localhost` to a production proof that deliberately accepts
only explicit numeric IPv4 endpoints. Later transport/edge steps did not run.
The correction is test-only: map the fixture's localhost to explicit127.0.0.1,
compare actual server/client addresses from an independent direct SQL connection
instead of assuming Docker NAT is127.0.0.1, and exercise local-socket proof only
when that socket actually exists on the runner. TCP proof stays mandatory;
production endpoint/admission guards are unchanged. A local service socket is
not inferred from a path reported by a remote service container.
Source: https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43386173504

Run43390305755 on307cd1b is also **not accepted**: web/security/Compose pass,
Go's new cache/pool history test canceled its sampler at100ms and found2of4
signals on the CI runner. Full117999-character raw inspected. Endpoint proof
now passes; later MQTT/Valkey/edge steps did not run. The test now waits for all
four real persisted signals with a15s bound, then cancels/joins the sampler.
No production sampler behavior, guard or assertion scope was weakened.
Source: https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43390305755

### Separate cache/enterprise CI acceptance (13 November,14:33IST)

Exact `5ee8d8df716881c11393034e7e2f30042093f5a4`, run43394130687 passes
all4 Go/web/security/Compose jobs. Full Go raw74261characters/736lines inspected.
The endpoint transport proof and real metric-history test pass in the full server
suite. Plaintext compiled-ingest MQTT and generated-ACL mTLS tests actually run;
the latter includes80events+8duplicate sends during600JWTlatest/series reads and
one extra TCP reconnect with publication paused. All80unique rows and final5079
freshness pass. Example runner mixed section4.03s/615HTTP responses/max11.72ms/
313cache hits is a bounded synthetic observation, not target capacity or an SLO.
Official hash-checked Valkey9.0.7 mTLS/ACL race test actually executes and passes
5generations x12readers, failed-flight recovery, expiry and endpoint outage bypass.
Edge tests/builds and linux-arm64/windows-amd64/windows-arm64 crossbuilds complete.

The full93-table backup/restore roundtrip remains a separate explicit local
opt-in test, not a claim that this CI ran it. Native8f0354c acceptance is also a
separate snapshot; no new native installer/Windows service evidence follows from
this Go/web/Compose run. Previous d0aaafc and307cd1b failures above remain recorded.
No owner deployment, main merge, release or full enterprise-readiness claim.
Source: https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43394130687
Go raw: https://github.com/abhisekrath00-ux/iot-platform/commit/5ee8d8df716881c11393034e7e2f30042093f5a4/checks/131780842169/logs

## Prepared update review product entry (local, not activation)

`setup.sh secure-update-review PROJECT RECEIPT` and the separate "Review prepared
server update" menu entry read the exact private application-only preflight
receipt and both immutable runtime snapshots. They show only project, plan hash,
writer names and immutable image IDs. Resolved environment, connection strings,
DB user, bind paths and credential/session/certificate bytes are not output.
The review runs no Docker command, callback, schema operator or service change;
no journal/selection file or Python bytecode is created. It does not create a
plan, approve downtime or authorize an update. Missing/changed inputs return
failure, not a legacy upgrade fallback. Live runtime and same-server dashboard
EXE are explicitly NOT verified. Production Upgrade execution remains separate.

The production adapter constructor and this review now share a pure file-plan
check. In addition to exact scope/installation/project/database/writer/image-only
change rules, both runtime paths must be present with matching hashes in the
manifest and the recorded current-image set must match the old plan. Hashing an
unrelated valid file cannot substitute for binding the runtime snapshots.

Local evidence:5review groups,8adapter,10Coordinator,10recovery,11provider and
10production-lifecycle contract groups pass; all31 non-native secure test files
pass. Current review5/adapter8 also pass Python3.14. Full Go tests pass on another
fresh PostgreSQL database.
Contracts also bind the exact previous/recovery read-only dependencies in both
manifests; omitted/extra recovery inputs and changed binds fail closed. Contracts cover secret-safe errors, missing/changed manifest/runtime/current
images/database/writers and no-Docker/no-file-write CLI/menu behavior. Actual
PTY menu output and synthetic plan CLI output were rendered and pixel-inspected.
These are local contracts, not a new native Docker or owner-host acceptance.

The next production activation gate is concrete: a fresh target/action/downtime
reviewer and mandatory live runtime/source/ingest/background acceptance plus
previous-image compatibility, not no-op callbacks. Retained gateway/profile/
device list equality alone excludes saved device connection config and many
scheduled behaviors. The gateway YAML download also writes an audit event and
contains generated source/rule config, so it is not silently added as a generic
read-only acceptance endpoint. The background set includes report/flow schedules,
automatic commands, KPI/geofence/escalation/forecast, retention/index, metrics and
calendar/on-call. Safe bounded acceptance needs defined semantics without
triggering real actuator or notification effects. Final matching server build,
dashboard EXE download/install and preserved identity/config evidence stay open.
