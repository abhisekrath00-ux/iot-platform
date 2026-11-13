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
| Cache handler failure | A handler panic formerly retained its single-flight entry indefinitely. Local correction releases only the failed flight, keeps partial output uncached and preserves the panic for HTTP handling. | Red regression reproduced for both modes, then cache race suite10repeats and real loopback HTTP recovery test pass. Cache race10repeats, real loopback HTTP panic/retry, and full fresh-Postgres Go suite pass. Local tested; separate CI pending, not a deployed fix. |
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
  including it also passes; separate remote Go/web/security/Compose CI is still required. Native v63 is a different exact snapshot and passes scoped lifecycle acceptance, not these later cache changes.

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
