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
| Read caching | `cmd/api/cache.go` gives live reads 2s TTL, configuration lists 10-30s, tenant invalidation after successful mutations. Customer-scoped users bypass shared tenant entries. `internal/respcache` bounds entries/body size, coalesces concurrent misses and refuses personalized/cache-control responses. | Local/shared unit and race tests; real-Postgres API tenant/invalidation test. Mixed dashboard/ingest/write load and failure recovery still need integrated evidence. |
| Optional shared cache | `internal/redisx` supports verified TLS, optional client certs, ACL credentials and DB selection. Shared read errors bypass caching, not stale local fallback. Generation invalidation spans replicas when the store succeeds. | Fake transport/failure tests are distinct from optional real `TEST_VALKEY_BINARY` test. No current real multi-node/failover evidence. Failed invalidation permits TTL-bounded stale entries; no stronger guarantee. No distributed miss lock. |
| Cache handler failure | A handler panic formerly retained its single-flight entry indefinitely. Local correction releases only the failed flight, keeps partial output uncached and preserves the panic for HTTP handling. | Red regression reproduced for both modes, then cache race suite10repeats and real loopback HTTP recovery test pass. Cache race10repeats, real loopback HTTP panic/retry, and full fresh-Postgres Go suite pass. Local pending publication, not a deployed fix. |
| Login state | `cmd/api/sessions.go` stores tracked JWT session IDs/revocation in PostgreSQL. Configured OAuth one-use state is a separate store; readiness depends on it when shared state is selected. | Do not describe all sessions as Redis-backed or use cache outage policy for authentication. Tenant/role/customer scope and revocation must remain checked before cached handlers. |
| Telemetry ingest | `cmd/ingest/main.go` validates topic-bound identity, inserts deduplicated telemetry, then invokes position/rules/flows synchronously. Duplicate inserts skip derived work. Reconnect reinstalls all subscriptions and refuses silent partial subscription. | Existing synthetic MQTT-to-PG-to-HTTP evidence is narrow. Need sustained ingest with dashboard/report reads, DB interruption, restart/reconnect and post-insert derived-work failure tests. No durable derived-work/outbox guarantee follows from QoS1. |
| Background work | API starts leader-elected report, flow, KPI, retention/index jobs using PostgreSQL advisory-lock ownership. Report concurrency has refusal limits rather than an unbounded queue. | Lock tests and component tests do not prove all jobs together under load. Need ownership loss, shutdown, bounded backlog, duplicate effect and restart evidence. Preserve explicit notification/control approval gates. |
| DB performance | Per-replica pool is configurable; telemetry has partitions/retention/rollups. Report load notes include a 2.4M synthetic row test and significant dead-row/vacuum sensitivity. | No customer capacity figure inferred. Capture query plans, pool waits, row growth, p95 and memory on a defined local fixture; test tenant fairness and overload refusal. |
| Observability | Health/readiness, logs and load harness exist; cache hit/miss/invalidation counters exist in code. | Must verify actual exported signals, alert thresholds and operator-visible stale/backlog states, not infer an operational monitoring system from counters alone. |
| Installation/update | Production provider now owns staging/proofs/cleanup with exact journal/image/manifest guards. Native Windows and Linux fixture evidence is recorded in `edge-secure-migration.md`. | v61 is last accepted scoped native fixture. v62 interpreter crash is unaccepted; v63 diagnostic rerun pending. Production entry/reviewer/menu integration is incomplete. |
| Backup/restore | Migration recovery protects the exact reviewed DB/runtime transaction and retained rows. Existing backup/restore tools are separately documented. | Recovery fixture is not a full deployment backup restore drill. Verify required DB/files/secrets, restore into a clean isolated install, retained identity/config/tenant data and a documented rollback path. |
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
- `server/cmd/api/{main,sessions,cache_test,edge_packages,edge_packages_test}.go`
- `server/cmd/ingest/main.go`, `server/internal/leader`, `edge/internal/queue`
- `web/src/components/{EdgeDownloads,EdgeDownloads.test}.tsx`
- `docs/{edge-secure-migration,backup-restore,report-load-test,slo,deployment}.md`

This review corrects stale architecture prose about Redis sessions, TLS and
outage behavior. Other historical readiness docs may still carry earlier scope
and must not be read as current approval or acceptance.
