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
EXE are explicitly NOT verified. The menu Upgrade hint now says legacy only / secure upgrade unavailable; the
action refuses secure state before any write and explains that plan review is
read-only, not approval. Production Upgrade execution remains separate.

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

## Leader lock-session loss (local follow-up, not c743 native scope)

A real local PostgreSQL fixture terminated only the dedicated advisory-lock
backend while the process/cooperative callback stayed alive. A second session
acquired the same lock immediately; the old job context remained live until the
5s watchdog (initial proof4996ms, takeover0ms). No notification, report delivery
or actuator effect was generated. This is a demonstrated overlap opportunity,
not proof of duplicate business effects. Graceful cancel and process SIGKILL
coverage did not exercise this failure mode. Session locks and cooperative
cancellation do not fence downstream effects or promise exactly-once execution.

The narrow local correction gives each watchdog check a2s context deadline,
cancels/joins the watchdog before unlock or pool reuse, bounds unlock/close and
closes the connection if unlock cannot be confirmed. It does not shorten the5s
check interval, replace the leader design, add infrastructure or eliminate the
connection-loss overlap window. Job/effect-specific fencing or idempotency is
still needed wherever overlapping effects are unsafe.

New bounded-watchdog timeout/cancel/join contracts and actual backend-loss/
cooperative-job-join/pool-recovery regression pass with the race detector over
3repeats alongside graceful and two-process SIGKILL tests. The regression logs
old-context state at successful takeover rather than asserting a scheduling-
dependent overlap duration. This local slice is not part of the pending c743
native acceptance; no owner deployment or combined update readiness follows.

## c743 native failure retained (13 November,16:45 review)

Exact c7432baf38bc1972dc7d5053f036d8110eafba24 general CI43407489890
passes all4jobs, but native43407540332 is NOT accepted. Linux failed at27m43s
in actual production-provider activation, before the planned lost-release
injection. Repeated exclusive-admission verification found the DB was not
exactly closed/zero-session behind local-only HBA. Transition remained blocked;
rollback refused the incomplete provider stage and no database restore ran.
Both Windows native jobs pass their scoped contracts/service lifecycle. This
is not the older v62 interpreter crash and no correction is inferred from it.

Full Linux raw log759452characters/5953lines retained. It proves prior forced
candidate rollback and lost-cleanup fresh recovery reached their assertions,
not the later activation/release success. The combined exclusive error did not
record which predicate changed; root cause remains unverified. A diagnostic-only
follow-up preserves every fresh guard and reports fixed failed-check codes for
closing stage, empty peers, closed database, zero sessions and local-only HBA.
It emits no raw catalog, session rows, HBA bytes, names or credentials. Injected
facet tests fail closed and assert redaction under Python3/default and3.14.
This is better failure evidence, not a timer change, retry or claimed fix.

## Local command publication claim, 13 November 2026, 17:08 IST

Unpublished follow-up to60f97e7. This does not change that tip's native validation
or resolve the c743 exclusive-guard failure. The earlier local reproduction
passed an already-sent command to dispatchCore and observed a new mock publish.
The old path read without an approved/unexpired predicate and published before
its conditional state update. Stale concurrent sweeper selections could both
reach publication even with cooperative leader cancellation.

The local patch atomically claims an approved, unexpired, tenant-scoped row as
outcome_unknown before publishing. A refused or unconfirmed claim never permits
publication. No automatic retry resets that claim after a process exit, publish
error or lost acknowledgement. The sent state means broker acceptance was
recorded, not edge execution or a physical outcome. An exact-identity gateway ACK
may settle an outcome_unknown claim before the API records sent; API finalization
does not overwrite it. The Control page explains unknown delivery without a
retry button, including on mobile without horizontal scrolling.

Evidence: fresh PostgreSQL with all93 migrations,12 concurrent callers yielded
one callback publication; terminal/pending/expired/cross-tenant rows yielded none;
a real child process exiting73 after its durable claim, lost publish acknowledgement and cancelled finalization retained
unknown state without replay. The claim race/child-exit test passed3 repeats
under the race detector. Early ACK survived API finalization. Full fresh
Go suite passed. Compiled ingest with a certificate-required local broker,
CN-scoped generated ACLs and two reconnects passed wrong-topic rejection and
exact-topic unknown-claim ACK settlement. These are synthetic replies and
callbacks, not output commands to equipment.351 web tests and production build
passed. Built UI screenshots with synthetic API responses were inspected at
1400x1000 and390x844. The first phone rendering hid status in a horizontal table;
the revised card rendering shows the complete warning.

Two initial full local runs failed the previous four-eyes test's failed-or-sent
expectation. That expectation now allows conservative outcome_unknown instead
of asserting that a publish error proves no delivery; the full fresh suite was
rerun successfully. No broker failure is silently retried or called acceptance.

Remaining: edge replay memory is process-local and resets on restart. MQTT QoS1
can redeliver after broker/client faults. The existing MQTT publisher still has blocking connect/publish waits rather
than a context-bounded send; no bounded-delivery claim follows. The server claim
is not an exactly-once actuation guarantee, and no real actuator or safe-retry proof was exercised.
Report/flow/notification effects, persistent edge replay protection, independent
hazard review and measured physical outcomes still need their own work. There is
no new automatic retry, command enablement or owner deployment in this slice.

Exact60f97e794aeba60c8433c61bf221de815350b509 validation subsequently completed:
CI43415919898 all4 jobs pass and native43415964761 all3 jobs pass, Linux46m30s.
Raw provider milestone assertions were inspected: forced rollback, released-tool
fresh recovery, committed original-HBA release loss and final settled recovery
passed. The819173-byte/6348-line Linux raw log is retained; only targeted test
and transition evidence was inspected, not every Docker trace line. Windows
service lifecycle and PowerShell contracts pass within their stated limits.
This does not explain the c743 red or prove the intermittent failure corrected.
The diagnostic predicates were unchanged; no failed facet was observed here.
- https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43415919898
- https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43415964761

## Local bounded command transport, 13 November 2026, 19:32 IST

Unpublished follow-up to exactd905. Commands previously used the shared retained
publisher's synchronous Connect/Publish token waits, without the caller's
cancellation boundary. The local change uses a dedicated clean-session,
non-retained QoS1 attempt, disables connect retry and automatic reconnect, and
bounds the attempt to15seconds or the caller's earlier deadline. Context-aware
dialing preserves configured proxy behavior; a proxy without context-aware
dialing is refused. TLS still requires the configured CA, client certificate
and hostname verification. Cancellation closes this attempt's actual socket,
including missing CONNACK/PUBACK; it does not disconnect the shared publisher.
The socket deadline also bounds stalled TLS/MQTT handshakes and writes.

Uncertainty remains uncertainty. A cancelled or timed-out attempt retains its
committed outcome_unknown claim and is not automatically re-armed. This bounds
the caller/connection, not device execution; packet delivery can precede a lost
acknowledgement. Existing non-command retained publication is unchanged.

Local proof: real loopback MQTT handshake fixtures, three race-detector repeats,
cover cancellation before CONNACK, cancellation after PUBLISH, missing PUBACK
at the deadline, ordinary success, certificate-required TLS success and refusal of wrong
hostname/missing client certificate. Socket
closure and explicit no-retry/clean-session options are checked. A fresh real
PostgreSQL test composes production dispatchCore, durable claim and a real
socket with withheld PUBACK: cancellation returns unknown, the socket closes,
the database stays unknown and a later dispatch refuses the existing claim.
Full fresh Go suite passes with the compiled-ingest mTLS pipeline.

A new real generated-ACL test initially failed to observe cmd delivery despite a
PUBACK. Investigation confirmed the API service CN is deliberately allowed only
diag/scan publication by the default generated secure ACL, not cmd publication.
That restriction is preserved. A same-publisher cmd/diag/scan barrier now proves
cmd delivery absent while allowed requests arrive; a separate dedicated
one-shot attempt succeeds on an allowed diagnostic topic with no executor.
The first failed local full run is retained. A second full fresh run passes
with the explicit denial and allowed-topic assertions. Do not call a PUBACK
proof of ACL permission, gateway receipt, actuation or measured outcome.

This is not permission to widen the secure ACL or enable output commands. The
secure generated-ACL command path remains unsupported, physical actuation
safety gates remain open, and no owner gateway or real equipment was involved.
Currentd905 native/CI acceptance does not cover this unpublished local patch.

Exactd905 validation has since completed: general CI43426162565 all4 jobs pass;
native43426236306 all3 jobs pass, Linux42m47s. Targeted raw assertions inspected
include provider rollback, lost-cleanup and committed-release fresh recovery.
The817410-byte/6327-line Linux raw log is retained, not every trace line read.
This validates that snapshot's synthetic fixture scope, not this later local
transport change or the unexplained c743 failure's correction.
- https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43426162565
- https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43426236306

## Persistent edge command admission candidate, 13 November 2026

Local follow-up, UNPUBLISHED. A synthetic reproduction reconstructed the gate
between deliveries and observed two simulated effects for the same request ID.
The previous map was process-local, not restart replay protection. New runtime
command modes require a sibling `queue_path.commands.sqlite` journal before the
MQTT command listener starts. Existing identity, configuration and telemetry DB
are not rewritten; empty command mode still rejects all effects. No command
mode, write allowlist, discovery or generated broker ACL is enabled or widened.

A validated envelope claims its request ID in a SQLite WAL transaction with
synchronous FULL BEFORE the actuator is called. Independent store connections
serialize claims; a failed/uncertain commit, closed/corrupt/busy store, capacity
limit or backwards persisted clock never falls back to the memory map. An abrupt
process exit after claim retains consumption, even if no effect happened. This
is conservative at-most-once admission until expiry, not an effect transaction,
exactly-once execution or a safe retry. Duplicate arrivals are refused locally, never replayed. Negative gate/storage,
no-actuator, cancellation/expiry and actuator-error observations are no longer
published as terminal rejected/failed ACKs: the old ingest maps those to failed,
which could misstate a prior effect after ACK loss or partial IO. Runtime logs
retain the local observation; the server stays sent/unknown awaiting trustworthy
evidence. This conservative loss of terminal refusal feedback is intentional, not
an exactly-once or successful-execution claim. Older edge negative ACKs remain
accepted by the server; upgrading this edge does not reinterpret history.

Expired claims are pruned only in the same transaction that advances a durable
clock high-water mark. A backwards clock is refused until caught up, preventing
an expired-and-pruned claim from becoming valid again after wall-clock rollback.
The journal caps live claims at100000 and request IDs at256bytes; full storage
refuses additional commands rather than dropping protection. WAL/SHM files are
part of SQLite's durability boundary. Deleting, losing, restoring an older copy
or moving the journal can lose history. No disk-loss, adversarial file rollback,
Windows ACL assurance or hardware durability proof is claimed. New file creation
uses0600 on Unix; inherited Windows ACLs still need deployment review.

Storage wait time is deducted from the remaining command lifetime before an
actuator starts, with a remaining-lifetime context for driver IO. Cancellation
cannot undo a write already sent. Multi-value/trailing JSON is refused. Existing
local actuator/write restrictions remain separate and unchanged. Policy-version
pinning is not implemented by this admission gate; earlier security-table wording
claiming it is corrected rather than treated as evidence.

Local evidence currently includes actual SQLite close/reopen;12independent
connections racing one request; real subprocess exit73 after committed claim
before actuation and exit74 after a synced simulated-effect marker; corrupt/closed/busy store refusal; expiry pruning then clock
rollback refusal;100000-entry cap; WAL/FULL settings; expired-during-claim and
already-canceled contexts; and composition with the actual Modbus actuator using
a FAKE writer. No equipment or wire writes. Race3 component tests and the full edge
suite pass. Fresh PostgreSQL server suite passes. Rebuilt edge/ingest pairing,
generated-ACL mTLS/unknown-ACK and source-apply/110reading pipelines pass. The
first pipeline invocation omitted this local broker binary's private shared-library
path and could not start it; the corrected invocation passes on another fresh DB. Windows-amd64/Linux-arm64
crossbuilds compile only, not native installation or final GUI/package acceptance.

### Separate c402 remote bounded-transport acceptance

Exact `c402dd140a05b4435115c87cede5b61b4c46d63c` completed general
CI43439566574 all4 jobs and native43439660828 all3 jobs, Linux51m18s.
Raw832349bytes/6464Linux lines retained; targeted provider transition/assertion
records were inspected, not every Docker trace. Forced rollback, lost host/local
cleanup fresh recovery, lost original-HBA-release acknowledgement and final
settled recovery without reviewed-schema replay pass. Both Windows jobs pass
scoped lifecycle/contracts. Go raw explicitly confirms default secure API cmd
PUBACK without delivery, allowed diag one-shot transport without executor,
mTLS/compiled-ingest reconnect/unknown ACK isolation and real Valkey transport.
This accepts that exact component snapshot only, not this later persistent-edge
candidate, full compatibility, equipment, owner upgrade or enterprise readiness.
The c743 failure remains unexplained, not proved fixed by another green run.
- https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43439566574
- https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43439660828

## Real dashboard-package verification candidate, 13 November 2026

Local follow-up to24cb, not a release or owner update. Previously the download
handler had fake-MZ fixture bytes, separately from the real package build. New
`scripts/test-edge-package-download.sh VERSION` builds all ten actual artifacts
and explicitly opts into `TestRealBuiltEdgePackagesDownload` using absolute
paths (Go runs tests from the package directory). A loopback TLS HTTP server
wraps the production inventory/download handler in real JWT signature/expiry
middleware. It downloads all ten files, checks exact byte equality against the
build, manifest version/size/SHA, headers and PE/ELF machine architectures,
refuses missing/invalid/expired/empty-tenant credentials, and rejects corrupt or
missing packages. The native Linux tarball's `-version` exits with the expected
build label without installing. This is not full session/revocation middleware,
code signing or publisher provenance. Shared installers intentionally have the
same bytes across tenants; the inventory is authenticated, not personalized.

Browser downloads now receive the selected inventory size/SHA and verify both
before creating an object URL or invoking save. Changed bytes, wrong size,
invalid inventory or unavailable WebCrypto fail closed. Verification therefore
requires a secure browser context (HTTPS or loopback); an insecure remote HTTP
lab does not silently skip it. This compares against the loaded inventory,
not an independent signature or safe-to-run guarantee. A server publishing a
new build between inventory and download causes refusal until the user refreshes.
The server also rejects trailing JSON/garbage after its single manifest object.

Optional `TEST_EDGE_PACKAGE_BROWSER_SCRIPT` runs the repository browser harness
as a child of that live handler test; it needs a supplied Playwright/Chrome and
built web output, and contacts loopback only. Local actual Chrome proof used
that production build, synthetic shell identity, and loopback proxy to the real
TLS/JWT handler. The browser saved the8,145,408byte Windows x64 EXE; an independent
hash matched the build. Flipping one byte AFTER handler verification produced
a visible SHA256 error and no second save. Desktop and390x844 action/error
pixels were inspected. The first long mobile element screenshot included fixed
shell overlays; final viewport screenshots show action and refusal separately.
No installer/UAC/signing/Windows GUI/enrollment/service change or equipment was
executed. The optional browser run does not contact an owner server/account.

Local evidence: real ten-artifact handler tests under race detector3repeats pass;
356web tests and production build pass. First harness invocation used a relative
path from the wrong Go working directory and failed to find its manifest; the
absolute-path wrapper passes. First TypeScript build rejected a test mock's
static method type; the typed mock and subsequent full build/tests pass. These
failures are retained as harness/type corrections, not product acceptance.
`test-edge-package-browser.cjs` is opt-in, not a claim that general CI runs
Playwright. Full fresh server suite and current exact remote validation remain
required before publication acceptance. Existing native24cb evidence is a
different exact snapshot and must not be silently inherited.

The joint handoff still needs a production secure-update entry with complete
runtime/background and previous-image compatibility, final matching server and
installer version, target Windows GUI/install/pair/source/test/save/apply/recent
reading checks, and physical/deployment acceptance. No optional Modbus mapping,
command ACL, owner setup, main branch, release or paid resource was changed.

## Open ingest-derived durability gap (2026-11-13)

The compiled ingest commits telemetry before position tracking, rules and flow
work. These steps are not one transaction or a durable outbox. If the telemetry
INSERT succeeds but a later derived operation fails, an exact replay with the
same `(event_id, observed_at)` is discarded before derived work. Restart does
not recover that missing work. Retained-list and HTTP-write compatibility
checks do not cover this gap. The production secure-update/full-background
acceptance gate stays closed.

`TestIntegrationCompiledIngestDerivedWorkGap` is a characterization of this
OPEN defect, not an assertion that ingest is durable. It starts the actual
compiled ingest and a disposable plaintext loopback broker against a disposable
PostgreSQL database. A trigger fails only the synthetic flow's run INSERT after
telemetry has committed. The test removes the fault, restarts ingest, replays
the identical event, then sends a unique below-threshold event as an ordered
barrier. Telemetry has two rows, the barrier has a derived run, and the missing
original derived run stays absent. No enabled notification channel, HTTP node,
function node or actuator is supplied. This proves one specific failure window,
not every rule, position, scheduling, notification or crash behavior.

Local race-enabled repetitions reproduce the gap. The first fixture attempt
accidentally regenerated `observed_at` on replay, which inserted a different
composite key and did not test exact deduplication. Freezing the envelope's
observation timestamp corrected the fixture. General unit tests skip this
opt-in process test; the existing compiled-ingest/mTLS pipeline test invokes
it as a separately named plaintext-loopback subtest, without changing workflow
permissions, runners or guards. A green subtest means the defect was reproduced.

Recovery is not enabled by these tests. Blind retries can repeat an external
notification or physical effect. A safe follow-up needs durable admission with
bounded recovery, explicit failure/uncertainty handling and per-effect replay
semantics, plus candidate and previous-image background compatibility evidence.
No existing command/ACL restriction, owner installation, main branch, release
or production secure-update availability changes here.

### Refuse flow notification scheduling when run recording fails

A separate real-PostgreSQL regression showed another unsafe branch: telemetry
flow evaluation logged a failed `flow_runs` INSERT, then still scheduled its
notification goroutine. A recording-only notifier received the synthetic
"reading 90" notification despite zero run rows. Evaluation now stops that
flow before scheduling notification dispatch when the INSERT fails, matching
the existing scheduled-flow guard. Healthy recorded flows still reach the
recording stub. No actual mail, webhook or actuator is used in this test.

`TestIntegrationFlowRecordFailurePreventsNotification` fails against the prior
code with the observed stub call and passes with the guard. Race-enabled
repetitions also cover existing latch rearming and cooldown suppression. The
negative dispatch check is bounded to300ms, followed by a healthy95-valued
control and a database record assertion; it is not an indefinite delivery
absence proof. The run's existing `notified` label still means scheduling, not
a recipient receipt.

This guard does NOT fix the missing-derived-work window above. It does not
add retries, a durable work queue or a new schema. Graph evaluation can perform
HTTP/context/control work before run recording, and delayed goroutines can be
lost after a successful record. Those remain explicit recovery-design gates,
as do exact candidate/previous-image background compatibility and owner update
acceptance. No command or automatic-output policy is widened.

### Position-source read failures are not empty configuration (2026-11-14)

The position tracker previously converted every source-config SELECT failure
into a successful empty configuration cached for a minute. Real-PG cancellation
returned success; a subsequent healthy reading could silently skip tracking
until cache expiry. Its cache also used device ID alone: a genuine negative
lookup under another tenant could suppress the owning tenant's later readings.
Both regressions fail on the preceding code.

The tracker now caches only a successful read or a genuine tenant-scoped
not-found result, keyed by `(tenant, device)`. Cancellation, lock timeout and
other database errors are returned without creating an empty cache entry. A
healthy reading immediately reloads after the failure without Reset or waiting
for TTL. Existing positive cache entries still have the documented one-minute
configuration TTL. A foreign tenant cannot reuse the owner's cached source.

`TestIntegrationPositionConfigReadFailure` uses a cancelled context and a real
exclusive table lock with a100ms query deadline, releases the fault, and checks
freshly paired coordinates/next latitude. The tenant-scope regression checks
foreign negative-cache isolation, no cross-tenant positive-cache reuse and a
genuine unknown-device no-op. Five race repetitions also cover existing source,
freshness and position-retention behavior. No live device or owner location is
used; coordinates are synthetic database records.

This is not durable position recovery. Previously lost readings are not
replayed; axis updates and trail recording are still separate operations, and
no journal or effect retry is added. The broader missing-derived-work and
secure-update/full-background/previous-image acceptance gates remain open.

### Open MQTT-to-database admission gap (2026-11-14)

The compiled ingest has a second, earlier durability gap: when a telemetry
INSERT fails, its callback logs the error and returns normally. The MQTT client
then acknowledges that incoming QoS1 message. Publisher-to-broker PUBACK is
already a different boundary; neither acknowledgement proves a database row.

The existing `TestIntegrationCompiledIngestDerivedWorkGap` now observes actual
subscriber wire packets through a loopback forwarding proxy. A scoped trigger
refuses only a synthetic event's telemetry INSERT. The test checks the real
ingest error, matches its subscriber PUBACK to that event's incoming packet ID,
and confirms zero stored rows. After fault removal, ingest restart and a unique
ordered-barrier event's derived run/PUBACK, the refused event is still absent
and its observed delivery count is one. This is a bounded observation of the
current clean-session configuration, not a proof of no redelivery forever or
of every broker/persistent-session policy. All traffic stays in a disposable
loopback fixture; no enabled notification channel or actuator is supplied.

Three local race repetitions reproduce the admission and post-commit derived
record gaps together. The existing mTLS pipeline invokes this separate
plaintext-loopback characterization; the wire proxy is not extra TLS proof.
A passing characterization means the defects remain reproducible, NOT reliable
admission or recovery acceptance. No MQTT acknowledgement behavior, session,
ACL, schema or effect retry is changed by this evidence slice.

A production recovery design must separately bind durable admission to its
acknowledgement boundary, preserve bounded pending work across failures and
classify effect uncertainty before retries. Disabling automatic ACK alone is
not a complete solution: clean-session loss, inflight backlog, shutdown and
broker persistence still need actual failure tests. Existing exact migration93
and candidate/previous-image acceptance gates must not be weakened to hide a
new journal or runtime change.

### Unwired PostgreSQL admission candidate (2026-11-14)

`internal/ingestjournal` prepares a bounded admission primitive. It is NOT
called by compiled ingest, API telemetry or product startup. Its candidate SQL
is outside the migration directory and is installed only by the disposable
integration fixture. The production migration ledger and exact93 upgrade gate
stay unchanged. No broker ACK, session, ACL, worker, lease, effect or retry is
activated. Both open runtime durability defects above still reproduce.

The candidate commits a telemetry row and its immutable pending-envelope record
in one database transaction. It canonicalizes UTC microsecond timestamps to the
existing global `(event_id, observed_at)` key and normalizes negative zero.
Exact journal replays return without re-inserting raw telemetry or rearming
work; changed payload/tenant collisions fail. Existing telemetry with no journal
is refused rather than inventing a new pending action whose prior effects are
unknown. A tenant-scoped read checks envelope integrity and routing columns.

A candidate-wide advisory transaction lock serializes admission and enforces a
100,000-record hard cap including all retained records. There is no pruning or
retention policy yet: filling the cap refuses new admission. This intentionally
conservative prototype is NOT throughput acceptance. Caller cancellation bounds
lock waits; any uncertain commit error requires reconciliation using the exact
same envelope, never inference that a write failed. Pending means admission
intent only, not permission, claim, derived completion or delivery.

Real-PG tests cover exact replay, changed payload/tenant refusal, microsecond
and timezone equivalence, independent fresh-pool reopen,12 concurrent same-key
admissions with one winner, failure of the second INSERT rolling back telemetry,
legacy-row refusal, raw-telemetry deletion without journal replay, cancellation,
12 contenders for the last capacity slot with one winner/11 refusals, corrupt
pending evidence and missing candidate table refusal without partial telemetry.
Three race repetitions pass locally. Reopening a pool alone is not process-kill,
commit-ack-loss, disk-failure or database failover acceptance. The separate
process-boundary fixture below covers only its stated transaction cases.

The journal retains the reading, not a reviewed rule/config version or effect
plan. No work executor exists. Integration still requires durable MQTT admission
and persistent-session/backpressure/shutdown tests, explicit staged migration
and previous-image compatibility, configuration binding, effect uncertainty and
operator recovery controls. This candidate does not enable production Update
or the held server/dashboard installer handoff.


### Unwired admission crash and one-attempt state model (2026-11-14)

A separate process-boundary fixture now runs the real candidate in child OS
processes through a PostgreSQL protocol forwarding proxy. Killing the child
before forwarding COMMIT leaves neither telemetry nor journal row. Killing it
after PostgreSQL emits COMMIT CommandComplete but before that response reaches
the child leaves exactly one pair. Dropping that committed response instead
returns an explicit uncertain-result error. Separate-process exact retries
reconcile absent/committed evidence without another admission. These are real
process kills and committed-response-loss cases, not fresh-pool simulations.
The proxy is a disposable plaintext fixture, not a TLS, disk-loss, database
failover, compiled-ingest or MQTT persistence test.

The candidate-only SQL and Go API now model `pending -> executing -> completed`
or `pending -> executing -> outcome_unknown`. A claim row lock admits at most
one random attempt ID. Every terminal checkpoint requires the matching tenant,
event, timestamp and attempt; canonical envelope integrity is checked before
transition. Completed means an explicit internal checkpoint, never receipt or
physical actuation. Neither terminal state can be claimed again or overwritten
by a late callback. There is no clock-based lease, expiry, automatic requeue,
takeover or cleanup. All retained states still count against the same hard cap.

A child killed after its claim commit but before receiving the response leaves
`executing` evidence on a separate connection. Another claim is refused; an
explicit matching-attempt quarantine makes it terminal `outcome_unknown` and
late completion/replay cannot rearm it. Twelve concurrent claimers yield one
winner; twelve competing completion/quarantine calls yield one terminal winner.
Tests cover foreign-tenant and wrong-token refusal, both terminal states,
corrupt-envelope refusal, cancelled claim and raw-deletion replay preserving
state. Three race repetitions pass locally. The first extended fixture failed
because its row counter required `pending` after explicit quarantine; the state-
aware assertion was corrected. No product failure was hidden or fixed there.

This state model has NO executor, immutable config/version binding, per-stage
plan, effect idempotency contract or operator recovery UI. It cannot safely run
or retry existing flow HTTP, notifications, control or context increments. SQL
remains outside migrations and product startup; exact93 guards and existing
runtime paths are unchanged. Both runtime durability gaps remain OPEN. Secure
Update, final matching installer, full-background and previous-image acceptance
remain separate held gates, not implied by these candidate tests.


The first remote process-fixture run on `c4dad0c` failed during SCRAM SASL
sign-in, before any COMMIT boundary: the proxy decoded authentication `p`
frames using the wrong password-message layout. Local trust-auth tests had
missed this harness defect. The corrected proxy forwards bounded PostgreSQL
frames byte-for-byte, inspecting only query/parse COMMIT and server COMMIT
completion. Unit tests preserve SASL initial/continuation and password frames
and refuse invalid/truncated lengths. Three real-PG race repetitions now pass
with an explicitly SCRAM-authenticated disposable role, including every process
boundary and one-attempt test. Authentication is not bypassed or weakened.
The failed remote run's downstream MQTT/mTLS/Valkey/edge steps were skipped;
it provides no acceptance for those paths. This correction changes only the
fault fixture, not product admission, work states, transport or migrations.

### Unwired published-flow evidence and static effect inventory (2026-11-14)

The admission candidate can now prepare evidence for ONE enabled published flow.
A single PostgreSQL statement joins its published-version pointer to the same
flow and tenant, requires published status, and captures version ID/number,
flow name and exact stored definition. The retained JSON bytes bind that data
to the canonical complete telemetry envelope and a versioned evidence policy.
A SHA256 detects substitution against the caller-retained original digest.
The digest is NOT a signature, permission or proof of who authored the data.

Inspection validates the current flow grammar and exact input stream without
calling Exec, a notifier, HTTP, context store or control requester. Every graph
node is conservatively inventoried, including conditional branches; this is
not an evaluated path or a per-occurrence effect plan. Split/join can visit a
node more than once. Unknown fields/node kinds fail closed. Legacy linear
notification flows are covered too; scheduled inject flows are outside this
telemetry-input candidate.

The inventory names external notifications, HTTP GET and POST (GET is not
assumed replay-safe), mutable context reads, context set/increment writes,
sandbox computation and control requests. Control is not assumed approval-only:
existing automatic alarm-output policy can dispatch after a request is raised.
It also names live flow-run history, notification channels/targets, feature and
egress policy, control policy/rate/pending state, context ordering, delayed
wall-clock dispatch and process-local rate limits as unresolved dependencies.
A pure computation upstream does not make a notification flow replay-safe.

Real-PG tests retain old evidence across publish-pointer/name changes, assert
new captures change, reject disabled/archived/cross-tenant versions and wrong
input streams, ignore mutable draft definition fields, and produce no flow-run
record. Unit cases bind tenant/event/time/value/device/point, preserve canonical
time equivalence, reject altered bytes/digests/unknown policy or grammar, and
classify the known effect nodes. Three local race repetitions pass.

This is PARTIAL configuration evidence, not complete immutable configuration
binding. Snapshots are not stored with admissions or required by Claim. Rules,
position tracking, live dependencies, exact side-effect occurrence identities,
revocation/authorization checks and operator recovery remain unresolved. No
product caller, migration, executor, automatic retry or MQTT ACK/session change
is added. Both runtime durability gaps, exact93 previous-image acceptance and
the combined secure-server/fresh-dashboard-EXE handoff remain open gates.

### Unwired retention of one flow-evidence selection (2026-11-14)

A separate `flow_binding_candidate.sql` prepares a candidate-only evidence table,
never a product migration/startup install. Its primary/foreign keys allow one
retained flow snapshot per existing admitted event. This intentionally cannot
represent all matching flows or the complete ingest configuration. It retains
up to 128 KiB per admission in addition to the bounded admission record; that is a
large worst-case storage cost, not an accepted production retention design.

BindFlowEvidence validates the exact input-bound evidence and locks its existing
tenant-scoped admission row, serializing with candidate Claim. New evidence can
be attached only while pending. An exact retained replay is inspection only in
any valid state; it cannot rearm work. Changed input/configuration, foreign or
corrupt evidence and late new bindings are refused, never overwritten or fixed
silently. Commit errors are uncertain and require exact evidence reconciliation.
ReadFlowEvidence joins both tenant identities and validates the retained bytes
against the canonical admission, not the latest mutable flow version.

Three real-PG race repetitions cover 12 simultaneous same-evidence bindings with
one inserted record, 20 concurrent bind-versus-Claim orderings per repetition, independent-pool inspection, changed input/config refusal,
terminal exact replay without rearming, late new-binding refusal, injected
storage failure with no half record/admission change, recovery after the fault,
corruption refusal and cancellation. No flow-run record or external effect is
created. Pool reopen is not process-kill/commit-ack-loss/disk/failover proof.

Claim is unchanged and still works WITHOUT a binding: this is a tested boundary,
not a new execution gate. Telemetry admission and configuration capture/binding
are separate operations, so publish changes between them remain an unresolved
window. All mutable dependencies and multi-flow/rule/position binding remain
open. No runtime caller, worker, retry, ACK/session change, additive migration
or owner rollout is enabled. Full background/previous-image and joint fresh
installer/server handoff acceptance remain blocked, not waived by retention.

### Published-flow loader identity/status fences (2026-11-14)

Inspection of the real telemetry and scheduled loaders found they joined a flow
only to its published-version pointer ID. The schema's single-column foreign
key does not ensure that the version belongs to the same flow or tenant, or
that its status is still published. Synthetic malformed DB rows demonstrated
that draft/archived, foreign-tenant-version and other-flow pointers could create
a run; native and Node-RED exports also returned those definitions. This is a
lab finding, not evidence of a malformed pointer or disclosure on an owner site.

Both runtime loaders and both published export queries now additionally join
same flow ID, same tenant and published status. These are fail-closed selection
checks, not a new schema, background worker, effect retry or configuration
snapshot guarantee. Correctly published matching definitions still execute and
export; disabled flows still export but do not execute. A foreign caller cannot
export another tenant's flow. Draft editing/export semantics elsewhere are not
redefined by these published-selection checks.

The new real-PG regression fails against the former queries for each malformed
pointer, and passes three race repetitions after the checks, for both telemetry
and scheduled definitions plus both export formats. All notification channels
are absent and the notifier is a recording-free stub; no external delivery or
device effect is supplied. Both runtime durability gaps and combined owner
update acceptance remain open.

### Stored-definition publish validation and serialization (2026-11-14)

Publish/rollback formerly looked up the selected version ID, separately read its
stored definition for the feature gate, and only then opened the transaction
that changed statuses/pointer. A failed definition read or JSON decode skipped
the gate. A real-PG regression demonstrates a syntactically valid but invalid
stored definition being published by those queries. This is a synthetic fault
finding, not evidence of malformed owner data or outside exploitation.

Publish/rollback now lock the tenant-owned flow and selected same-tenant/flow
version in one transaction. The exact locked stored definition must decode,
validate and pass the existing HTTP/control/function admin/feature/code checks
before changing any status or pointer. Feature lookups use that transaction's
connection, not another pooled connection while locks are held. Update row
counts are checked. Per-flow locking serializes competing pointer changes and
only that tenant's versions are archived. No execution or dispatch occurs here.
These checks do not freeze mutable feature policy for later execution or supply
a durable work/config snapshot. A commit failure is still an uncertain result;
inspect the flow/version state before another publish attempt.

Three local real-PG race repetitions cover invalid stored array/object/unknown
step refusal for publish and rollback without pointer/status change, injected
pointer-update refusal rolling back status changes, ten pairs of competing
publishes with one current published version and matching pointer, and normal
rollback. Function/HTTP/control feature-off admin and enabled-feature operator
are refused; enabled-feature admin publishes configuration using a one-connection
pool with a bounded request context. No evaluator, notification client or device
is invoked, and flow_runs remains empty. This is local transaction acceptance,
not crash/commit-ack-loss, full owner deployment or either durability-gap closure.

### Isolated candidate subscriber admission boundary (2026-11-14)

A separate test-only child process combines candidate Admit with an explicit
manual subscriber PUBACK, a stable client ID and a non-clean MQTT session. It is
NOT compiled ingest or a new product option/worker; it does not change production
MQTT configuration, topic validation, ACK/session behavior, schema or effects.
The harness supplies complete synthetic envelopes on a disposable loopback topic,
not the production envelope resolver or broker identity/ACL path.

A real loopback broker and packet-decoding forwarding proxy observe the
subscriber's actual PUBACK separately from publisher-to-broker acceptance. A
scoped trigger refuses a telemetry INSERT: the callback reports failure, neither
telemetry nor pending record exists and no subscriber PUBACK is observed. Killing
that child, removing the fault and reconnecting the same persistent subscriber
redelivers the input, commits one telemetry/pending pair and then emits PUBACK.

For another input the child stops after successful Admit but before ACK. The
parent confirms the committed pair and no ACK, kills the process, marks its
single candidate attempt outcome_unknown, then restarts the subscriber. Exact
admission replay is acknowledged without adding telemetry/work or rearming the
terminal attempt. An ordered unique barrier and three readings published while
the subscriber is offline are each admitted/acknowledged once on restart.
These are a tiny bounded queue experiment, not backlog capacity acceptance.

Three local real-PG race repetitions pass. A fresh-DB compiled-ingest mTLS suite
also passes with this separate plaintext candidate experiment as an explicitly
named subtest. That subtest is NOT extra TLS proof. The same run still reproduces
BOTH open defects in actual production ingest. This initial variant keeps its
broker alive with in-memory session persistence only.

A separate candidate test variant enables Mosquitto file persistence in a
disposable directory. After the subscriber stops and three readings enter its
offline queue, the test gracefully terminates the broker, requires a nonempty
persistence file, and starts a new broker process using the same config/data.
The subscriber reconnects and each queued input produces one pending pair and
one observed PUBACK. Three real-PG race repetitions pass; the explicitly named
CandidatePlaintextGracefulBrokerRestart subtest also passes in the fresh-DB mTLS
suite. That experiment remains plaintext and test-only, not extra TLS proof.
Broker shutdown is bounded by a timeout, not an unbounded wait.

Graceful broker file reload is not abrupt broker-kill/power-loss/fsync/disk-full
acceptance, a production persistence configuration or a safe shutdown policy.
Shared subscriptions, stable client-ID allocation for replicas, backpressure,
sustained load, MQTT identity and full config/effect binding remain unaccepted.
Candidate evidence does not close either runtime durability gap or enable the
held owner update/server/fresh-installer handoff.

### Candidate broker checkpoint versus abrupt process kill (2026-11-14)

A third, separate plaintext test-only variant enables broker file persistence
with automatic saves explicitly disabled. With the candidate subscriber offline,
three queued readings are checkpointed using broker SIGUSR1; the test waits for
a nonempty persistence file modified after that request. It then publishes a
fourth input and observes publisher-to-broker acceptance, without another save,
and SIGKILLs the broker. A new broker process on the same config/data reloads
the session and three checkpointed readings: each produces one candidate pending
pair and subscriber PUBACK. The publisher reconnects and sends a unique ordered
barrier, which is admitted and ACKed. The fourth, uncheckpointed input has no
subscriber delivery, database pair or ACK. This is an intentionally reproduced
OPEN CANDIDATE TRANSPORT GAP, not a production incident or default-policy claim.

Three local real-PG race repetitions pass for both graceful and kill variants.
The separately named CandidatePlaintextCheckpointedBrokerKill subtest passes
inside the fresh-DB compiled-ingest mTLS suite but is NOT additional TLS proof.
This accepts only checkpointed broker process recovery on the same local files;
it does not accept general abrupt crash, power-loss/fsync, disk-full, replicas,
capacity or production MQTT/persistence settings. Publisher acceptance cannot
be promoted to durable database admission by the candidate journal alone. No
runtime option, broker config, schema migration or worker is added. Both actual
production durability defects and combined owner-update/installer gates stay open.

The first remote graceful-restart run (7f1d1ce,43593455945) failed: its CI broker
launcher mounted the entire fixture directory read-only, so no persistence file
was created. Local native-broker acceptance had not covered that launcher.
Valkey and edge steps after the failed mTLS step were skipped, not accepted.
The corrective harness uses a dedicated writable broker-data submount while
keeping config/certificates read-only. Explicit container-ID-based broker
signals distinguish killing the actual broker from killing the Docker client;
checkpoint-kill requires native SIGKILL or container exit137. Native-broker race
and fresh-suite checks do not by themselves accept this Docker correction; the
next exact remote run must execute the named tests and downstream steps.

### Separate staged processing schema path (2026-11-14)

The corrected f5a337d CI43599337939 is all-four green. Both graceful broker file
reload and checkpointed SIGKILL subtests execute under the corrected Docker
launcher; Valkey and edge tests/cross-builds previously skipped now pass. This
closes the fixture portability issue, not the documented unsaved transport loss
or either actual production durability defect.

The next product-path prerequisite is a separately embedded, staged processing-v1
schema. It is NOT in server/migrations, so normal API startup and the existing
Update adapter still package exactly93. setupctl has distinct inspect-staged-
processing/apply-staged-processing actions; the latter requires an explicit
staged_processing_v1 maintenance acknowledgement, a readable exact93 catalog,
exact core ledger/object checks, bounded shared migration lock and no other
current database clients. Acknowledgements are not owner permission or proof
that an external client cannot reconnect. The current Update adapter never calls
these actions. No API/ingest startup applies this SQL or activates processing.

One additive table binds bounded immutable input/plan bytes, digest, versioned
policy and pending/executing/completed/outcome_unknown attempt state. A separate
catalog checks embedded SQL bytes and exact columns/constraints/indexes; unknown
ledger entries, unrecorded objects, altered core/staged shape and extra triggers
fail closed. DDL and the94 ledger entry commit together. A commit error requires
fresh inspection; no delete, schema downgrade or restore action is provided.
The current exact93 operator still REFUSES94, as intended, rather than silently
accepting a widened Update contract. The extracted core shape reader preserves
its existing93 comparison; its full regression suite runs unchanged.

Three local real-PG race repetitions pass for staged apply/inspection, repeat
inspection, legacy93 refusal, injected ledger failure with all DDL rolled back,
retained identities/source config, unknown/altered object refusal, other-client
refusal and cancellation. An optional local script builds the exact bfe5d89
previous API and current API/setupctl. The actual local staged operator applies94
in an isolated DB between stopped API processes. Previous API before94, current
API on94, previous API on94 and current again boot successfully and perform
JWT-authenticated retained gateway/profile/device reads plus HTTP telemetry
write/retry, retaining four unique readings. The new processing table stays empty
and both runtime_accepted and processing_enabled remain false. This is limited
actual-binary/default-path compatibility, NOT immutable-image/full-background,
backup/restore, complete rollback acceptance, runtime worker or effect recovery.

The staged table is a prerequisite, not a worker, complete configuration plan or
safe execution decision. Complete input identity/config/occurrence binding,
transactional internal stages, one-attempt external effects with visible unknown
outcomes, execution-time revocation, session/backpressure/shutdown and end-to-end
admission receipt remain required. No automatic external-effect retry is added.
Both actual runtime durability gaps and combined owner-server/fresh-EXE handoff
remain open. Nothing in this path changes existing deployment authority.

### Default-off product admission and retained planning quarantine (2026-11-14)

`internal/ingestprocessing` now implements the actual processing-v1 admission
operation against the separate staged94 table. No startup caller, MQTT handler,
HTTP ingest endpoint or Update path calls it. A zero-value Engine refuses before
DB access. Enabled instances exist only in isolated acceptance tests.

Admission validates a bounded reading with an explicit stable timestamp and
commits telemetry plus its retained configuration selection/plan together in one
serializable transaction. Conservative global admission serialization, SQL/lock
bounds and a100000 retained-row cap fail closed; serialization or commit errors
require reconciliation, not external-effect retries. Legacy telemetry without a
plan is not backfilled. Conflicting identity/payloads refuse. Exact duplicates
return the first retained plan/state before reading today's configuration, even
when the state is outcome_unknown, config has changed, or capacity is now full.

The single snapshot selects tenant/device/gateway binding, position-point config,
maintenance decision, bounded tenant feature policy, every enabled rule and flow
with its exact published binding/definition, and all tenant channels including
disabled ones. Cross-tenant/broken published pointers do not silently disappear.
Limits:128 rules/flows/channels each,64 features,64KiB per definition,8192-byte
channel targets,1024 derived occurrences/reasons and256KiB total plan. Oversized
selection refuses the entire admission, with no telemetry-only commit.

Only fully classified stateless flow nodes are evaluated to derive occurrence
intents; HTTP/context/control/function/rate-limit nodes are classified BEFORE
Exec and never run. Position/alert transactions, history/scoped rules, flow
latch/cooldown and unsupported adapters are explicit durable planning quarantine,
not skipped success. A blocked stage prohibits execution of the entire plan.
The plan is a complete bounded selection with explicit unsupported dependencies,
NOT an implemented complete executor for every stored automation. SHA256 checks
integrity, not permission. Replay re-derives intents/reasons from the retained
selection and rejects mismatches even with a recomputed checksum. Tenant-scoped
Read exposes pending/unknown and planning reasons while processing stays disabled;
no owner UI or recovery button is claimed yet.

Real-PG tests cover paired rollback on processing-insert fault, conflicting and
legacy inputs, concurrent exact duplicates, old/new configuration snapshot
consistency, changed-config/unknown-state replay, tenant isolation, cancellation,
capacity including terminal records, bounded selection and explicit unsafe-node
quarantine. No notifier, HTTP/control/context runner or legacy void evaluator is
called. Three local race repetitions and fresh full-server/vet checks pass.

Still open: transactional internal stages, per-effect durable attempt/receipt
records, execution-time authorization/revocation, one-attempt external execution,
visible operational recovery, stable clockless transport identity, all-handler
manual-ACK/session/backpressure/shutdown, application admission receipt/edge
outbox, immutable-image/restore/rollback acceptance and combined server/fresh EXE.
This code does not fix either enabled production durability defect or the
publisher-accepted unsaved transport loss. Existing exact93 guards are unchanged.

### Default-off one-attempt stateless-flow batches (2026-11-14)

The same product package now implements guarded ExecuteOnce for an unblocked
stateless-flow plan, still with NO startup, MQTT/API ingest or scheduler caller.
A reviewed one-attempt Sender adapter is required. Only synthetic isolated test
senders exist; existing production SMTP/Slack adapters are NOT wired or accepted
for this path. In particular their timeout/provider-result behavior is not
silently assumed safe. No notification or device effect was sent during tests.

A serializable claim transaction locks the input, validates retained plan/state,
refuses unsupported stages, compares the complete current configuration against
the retained selection, writes deterministic input+flow history and sets one
attempt UUID/executing state together. No effect happens before that commit.
A failed/uncertain claim commit returns no send permission. History says
processing_started, not notified. There is no retry lease, expiry, takeover or
background requeue. Delays stay tied to durable selection time rather than
starting afresh after each wait. Before each callback a fresh consistent current
configuration check refuses any disabled/changed source, target or policy;
targets are never substituted. This is the authorization check point, not an
atomic transaction across the database and an external provider.

The deliberately conservative policy is ONE ATTEMPT FOR THE WHOLE BATCH. If a
sender error, cancellation or checkpoint failure occurs after claim, the batch
is recorded outcome_unknown when possible. All occurrences are quarantined,
including those never started. A crash leaves executing/attempt evidence with
an explicit no-takeover/no-retry inspection label; it cannot be claimed again.
This policy avoids duplicate external effects by accepting potentially missing
work. It does NOT claim per-effect durable receipts or exactly-once delivery.
Completed means the batch's provider-acceptance callbacks/checkpoint finished,
NOT recipient delivery, actuation or a guarantee every possible flow is supported.

Supported flow history and the final batch checkpoint update transactionally.
If a source is deleted mid-attempt, quarantine can still be recorded even though
its cascading flow history is gone. A failed final completion cannot rearm the
batch. No old evaluator's HTTP/context/control side effects are replayed.
Matched rules/position/history-dependent flows remain planning quarantine until
their own transactional stages exist; they are not bypassed to run the rest.

Local real-PG tests cover durable claim/history before callback, one winning
concurrent claim, refusal of changed policy before/between callbacks, no
retargeting, stopped remaining callbacks after uncertainty, cancellation during
delay, atomic internal failures, final-checkpoint faults and no rearm. Actual
child processes are SIGKILLed after committed claim and after a synthetic provider
receipt; zero/one receipts respectively remain, and neither restart/replay nor
another execution call can resend or take over the attempt. This is isolated
local process proof, not real provider or production transport acceptance.

Final local validation: three real-PG race repetitions of admission+execution,
ten non-race process-kill repetitions, fresh full-server suite and vet pass.
The first full-suite attempt caught a child-fixture lifecycle race (after-claim
helper could exit normally before the parent's kill); the helper now stays alive
and the parent verifies actual stopped process state before SIGKILL. The final
fresh full run passes; the earlier failed run is not accepted evidence. No
product policy or test assertion was weakened. No remote acceptance is inherited.

### Default-off transactional position stage (2026-11-14)

The processing-v1 claim now applies supported position metadata in the SAME
transaction as flow history and its executing/attempt checkpoint. Position-point
configuration is retained at admission and current policy must still match;
there is no cached or substituted point mapping. Per-device row locking retains
monotonic per-axis observation times, the existing two-minute pairing window,
hand-set coordinates until a fresh pair, and no-op handling for invalid/stale
axes. Sampling uses the existing trail distance/heartbeat/retention operation,
now exposed as RecordTrailTx without committing its caller's transaction. The
existing RecordTrail wrapper still owns its own transaction and current runtime
PositionTracker still uses its previous post-commit best-effort trail behavior;
this default-off stage does NOT silently fix the enabled production path.

A trail failure or attempt-state failure rolls back the axis, published position,
trail, internal history and claim together. A committed claim that is then
interrupted retains the position and the unclaimable attempt; there is no second
metadata or external attempt. Concurrent axes serialize on the same device row;
an unstarted serialization loser remains pending, not implicitly completed.
Unsupported rules/history/side-effect nodes still quarantine the entire plan
BEFORE any position change. No geofence evaluation or notification is added here.

Three local real-PG race repetitions cover pair/stale/equal-time/invalid axes,
wide pair windows, source revocation, retained manual coordinates, trail/state
fault rollback, concurrent axes and caller rollback. The stage remains default
off with no API/MQTT/startup caller. Rule/history stages, real sender adapters,
operational UI, transport/receipt and deployment acceptance gates remain open.


### Read-only retained processing evidence view (local, 2026-11-15)

System health has a collapsed **Staged processing evidence (read only)** panel.
An interactive workspace admin can inspect one exact event ID and original
RFC3339 observation timestamp. Opening the panel, loading the page, or editing
identity does not fetch evidence; only the explicit Inspect button makes a GET.
The API refuses API keys, customer-scoped admins and non-admin roles. The query
is tenant-scoped, bounded to one exact identity and five seconds, sends no-store,
and never applies a schema, claims work, repairs state or invokes execution.
An absent staged table is reported as absent, not installed on demand. Missing
records do not prove the input was never received or processed; failed or invalid
integrity checks remain unknown, with no automatic retry.

The response validates the retained canonical envelope/plan/state using the same
read path as processing-v1. It returns identity, policy/selection time, state,
attempt ID, counts and planning/attempt quarantine reasons only. Destinations,
message bodies, definitions and raw plans are omitted. Pending is not execution;
executing and outcome_unknown prohibit takeover/resend; completed means batch
callbacks/checkpoint finished, not recipient delivery or device actuation.
Editing identity clears stale evidence. A failed refresh also clears it, and a
slow older response cannot refill another identity. Microseconds are compared,
not silently rounded to browser milliseconds.

Local isolated PostgreSQL/race tests cover absent staged schema, tenant/role/key/
customer boundaries, exact identity validation, real immutable admission and
unsupported-history quarantine, response redaction, state inspection and bad
plan-digest refusal. State-display cases are synthetic retained SQL fixtures;
they do not assert an actual provider sent anything. Chromium at1440/390 tests
pending/unknown/completed labels, five explicit read-only checks and no writes,
microsecond-mismatch refusal, cleared evidence after failure, no page overflow
or browser errors; those browser responses are synthetic API fixtures.

This is an inspection control, not operational recovery or a working enabled
production processing pipeline. There is no Activate, Retry, Resume, Send or
Take over button. Current MQTT/HTTP/legacy evaluators and exact93 Update guards
remain unchanged. Production ACK/derived-replay defects, broker durability,
real sender adapters, admission receipt/edge outbox, immutable-image restore/
rollback and owner-update acceptance remain open. Inspect the exact published
commit's full CI before treating local tests as remote acceptance.

### Default-off one-attempt Slack adapter (local, 2026-11-15)

The staged processing package has a separate explicit-credential Slack sender,
with NO environment lookup, startup/MQTT/API worker wiring or change to the old
production notifier. Its constructor fixes the official HTTPS chat.postMessage
endpoint, normal certificate/hostname verification, TLS1.2 minimum, no inherited
proxy, bounded dial/header/body/total time, no redirects, no reusable connections
or retry path. Exact C/G/D conversation IDs only: names and U user IDs that might
open a DM are refused. Text is nonempty valid UTF8, at most4000 characters; explicit
mention/channel markup is refused and automatic expansion/markup/link/media
unfurls are disabled. No identity override, alternate recipient or remote lookup.

HTTP200 alone is not acceptance. The complete response is at most64KiB, valid
JSON without duplicate or case-colliding keys, bounded nesting, literal ok=true,
exact retained channel and a valid message timestamp, with no truncation/warning.
Refusal, rate limit, timeout, partial response, redirect or lost response returns
a generic unknown result without provider body, token or automatic retry. Through
ExecuteOnce, the whole claimed batch becomes outcome_unknown and cannot be
rearmed, including remaining occurrences not attempted. A changed channel policy
between two requests prevents the second request. Completed is provider response
acceptance only, not recipient delivery. No per-effect durable receipt is added.

Current Slack semantics checked against its method documentation:
https://docs.slack.dev/reference/methods/chat.postMessage/ . Tests use only local
synthetic TLS HTTP servers and synthetic credentials, plus isolated PostgreSQL
for actual claim/history/attempt/no-rearm checks. They cover success, false/missing
ok, wrong channel, missing timestamp, warnings, duplicates/case collisions,
oversize/deep/trailing JSON, redirects, dropped response, rate limits, cancellation,
body/header timeout, invalid audiences/text and untrusted TLS. Three local PG race
repetitions and a fresh isolated-database full server suite/vet pass. An initial
full run on reused development PostgreSQL hit retained fixture-ID conflicts and
is not acceptance evidence; the final fresh-database run passed. No real Slack credential/channel, actual provider acceptance,
SMTP support, production activation or delivery claim follows from these tests.
Enabled production ACK/derived-replay defects and transport/deployment acceptance
remain open; exact93 Update behavior is unchanged.

### Default-off mandatory-TLS SMTP adapter (local, 2026-11-15)

The same staged package has an explicit SMTP sender; no production notifier or
startup/transport worker is changed. The constructor requires either implicit
TLS or STARTTLS with normal certificate/hostname verification, TLS1.2 minimum
and an explicit host/port/sender. There is no plaintext fallback, insecure TLS
option, environment lookup, MX discovery or automatic resend. Optional PLAIN
credentials are used only after verified TLS and advertised AUTH PLAIN support;
explicit empty credentials select a TLS relay without AUTH. Invalid or partial
credentials/configuration are refused rather than silently downgraded.

Each occurrence opens one connection with bounded dial and total context/socket
deadlines, closes on cancellation and caps total inbound SMTP/TLS bytes64KiB to
bound multiline replies. It sends one exact ASCII mailbox recipient, one fixed
sender and one DATA transaction. Valid bounded UTF8 text is quoted-printable MIME
under constant headers, without HTML, attachments, CC/BCC or recipient lookup.
A final DATA acknowledgement establishes relay acceptance only. Lost/failing final
responses quarantine the whole batch, including unstarted occurrences; no retry,
RSET/resubmit or recovery send follows. Successful DATA is not turned unknown
because of a later QUIT failure: the connection is simply closed. No recipient
delivery, durable per-effect receipt, mail queue or production activation claim.

Synthetic local TLS SMTP fixtures plus isolated PostgreSQL cover implicit TLS and
STARTTLS, exact MIME/body/recipient, optional explicit no-AUTH relay, missing TLS
or AUTH extensions, wrong hostname/untrusted CA, injection/list/display-name input
refusal, auth/MAIL/RCPT/DATA/final refusal, oversized greeting, canceled/stalled
connections, lost final receipt, actual committed claim/history before DATA and
revoked-between-occurrences refusal. Uncertain attempts cannot rearm. Three local
real-PG race repetitions, final fresh-database full server suite and separate vet
pass. An initial fixture did not handle SMTP auth cancel;
that fixture was corrected. The first extension-negative test also caught missing
explicit AUTH capability validation; the product now refuses before sending AUTH.
Neither failed test run is acceptance evidence. No real email/provider/credential
was used. Standard library protocol semantics checked at https://pkg.go.dev/net/smtp .
Production ACK/replay defects, admission/outbox, staged94 image/restore/rollback,
real-provider acceptance and combined server/EXE deployment gates remain open.

### Default-off mixed SMTP/Slack batches and preflight (local, 2026-11-15)

NewNotificationSender joins the separate staged SMTP and Slack adapters under
explicit copied configuration, with no environment lookup or provider call in
the constructor. Only the configured email/Slack capabilities are available;
unknown types, missing adapters and invalid targets/text are refused. The
complete retained intent list is preflighted inside the claim transaction BEFORE
position metadata, flow history or attempt state can change. This pure check
makes no network calls and does not prove credentials, authorization or delivery.
Direct built-in SMTP-only/Slack-only senders perform the same full-plan check;
a mixed batch cannot partially send before discovering an unsupported later
occurrence. Package-private preflight cannot be implemented by external packages.
The engine still permits separately reviewed synthetic/test Sender implementations.

The existing authorization check still runs after preflight and immediately
before each callback. Dispatch preserves exact retained order and targets, never
substitutes a channel, and uses the same one-attempt whole-batch quarantine:
uncertain SMTP/Slack responses or a policy change stop all remaining occurrences,
including those not started. Neither a new sender nor duplicate input can rearm
an executing/unknown/completed attempt. There is no automatic retry, fallback to
a different provider, per-effect receipt, worker, API/startup wiring or activation.
Enabled production ingest/legacy notifier/exact93 guards remain unchanged.

Isolated PostgreSQL with actual local TLS SMTP+HTTPS fixtures exercises
email -> Slack -> email order, position/history/claim committed before first
network operation, mixed success, SMTP lost final receipt, Slack refusal and
revocation after either provider. Missing/typed-nil adapters and invalid later
recipients fail while input stays pending, without position/history/attempt or
network changes; repeated refusals release the transaction/row lock. Constructors
copy configuration and do not dial; unknown channel types and leaked input detail
are refused. Three real-PG race repetitions, fresh full-server suite (four package
workers) and vet pass. An initial unbounded-package-concurrency full command hit
the execution time budget and was not accepted; the final fresh run exited0.
These are synthetic protocol
endpoints, not real-provider acceptance, delivery or enabled-pipeline durability.
Full transport admission/ACK/outbox, rules/history stages, per-effect evidence,
immutable staged94 images/restore/rollback, GUI click/UAC/reboot/physical meter
and the joint secure-server/fresh-EXE handoff remain open.

### Default-off strict MQTT telemetry admission boundary (local, 2026-11-15)

Engine.AdmitMQTT binds the existing transactional processing-v1 admission to a
bounded exact telemetry wire envelope and topic identity. The caller MUST already
have authenticated the publisher and enforced broker ACLs; parsing a topic string
is not authentication. No production handler, startup or ACK mode calls this
method. Existing enabled MQTT acknowledgement/replay defects are NOT fixed.

The boundary rejects unknown/case-folded/duplicate JSON keys, null or missing
required fields (including value), invalid UTF8, oversized payloads, wildcard or
malformed topic identities and payload/topic tenant/gateway disagreement. Topic
identities may be omitted from the body but cannot be substituted. It requires an
original stable publisher timestamp exactly representable in microseconds, with
NO receive-time fallback or silent rounding. Current sources emitting arbitrary
nanoseconds/clockless readings need a separately reviewed stable wire identity;
this boundary does not pretend they are compatible or change their output.

A receipt is returned only after the existing Admit commits or reconciles an
identical retained input. It contains exact event/time, retained state and new vs
replay only, no plan/targets/message text. Pending/unknown/executing/completed
remain distinct and do not authorize execution, rearm, MQTT PUBACK or an edge
application receipt. Admission makes no derived or external call. Database/commit
errors return a zero receipt, including uncertain outcomes; explicit exact-input
reconciliation is distinct from automatic external-effect retries. Replays return
the original plan/state even after configuration changes or terminal attempt state.

Isolated PostgreSQL tests cover commit/insert refusal with zero receipt and paired
rollback, legacy/no-journal input, identity conflict, foreign topic, cancellation,
absent staged schema, strict wire rejection, concurrent exact admissions and
explicit loser reconciliation. Three race repetitions, a fresh full-server suite
(four package workers) and vet pass. An initial legacy
fixture omitted telemetry's required unit and was corrected; only final passes
are accepted. No broker/session/manual ACK, all-handler/backpressure/shutdown,
edge outbox, actual publisher retransmit or power-loss durability is claimed.
Production activation, staged94 immutable-image/restore/rollback, rules/history
stages, per-effect evidence and joint server/fresh-EXE readiness remain open.

### Isolated MQTT admission gate (default-off, no subscriber wiring)

`NewMQTTGate` now constructs an explicitly enabled Engine's bounded synchronous
adapter with 1-64 admitted handlers. Zero/nil gates refuse. It accepts only
non-retained QoS1 messages, copies the bounded payload and invokes that message's
`Ack` only after `AdmitMQTT` returns committed or exact-replay evidence. Failed
insert/commit, missing schema, malformed/foreign input, legacy/conflicting input,
cancellation and capacity/closed refusal return zero evidence without ACK.
Each duplicate delivery may need its own protocol ACK; no derived work is run.
ACK invocation is not proof of broker receipt, edge application acceptance or
provider delivery. An interrupted ACK is uncertain; exact-input reconciliation
retains one admission without rerunning effects.

Capacity refusal is immediate, with no internal queue, worker or retry. Close
permanently refuses new calls and waits for admitted handlers including ACK to
return. A timed-out Close leaves the gate closed and may be called again to wait;
it does not cancel in-flight transactions or prove rollback. Caller-owned
contexts bound their work. Real isolated PostgreSQL tests observe an admission
blocked on the actual advisory lock, refusal at capacity before commit, canceled
rollback with no ACK, and drain during a blocked ACK after commit. They also cover
concurrent Close calls, timeout remaining closed, ACK panic releasing capacity,
commit-visible independent reads during ACK, exact replay and the refusal matrix.
Three race repetitions and a fresh full-server suite/vet are the local gates.

This is NOT an all-handler/manual-ACK production fix. No production subscriber
constructs the gate, changes AutoAck/session mode or switches telemetry. Its caller
still must establish authenticated topic ACLs, manual ACK, stable persistent
sessions, all-handler failure/shutdown behavior and broker/publisher durability.
Returning a refused-message error alone does not make Paho redeliver it. No real
broker/PUBACK recovery, source retransmit/outbox, GUI/native installer, hardware,
provider delivery, owner activation or staged94 image/restore acceptance is
claimed by these adapter tests. Existing runtime ACK/replay and unsaved broker
input-loss findings stay open. Existing exact93 update guards remain unchanged.

The first remote gate successor `de0f568` normal CI43738528642 was RED in
`TestReportGateWaitsThenGetsSlotAndHonoursCancel`. Other jobs passed, but later
Go edge/mTLS steps were skipped and that tip is not accepted. Investigation found
an existing test reading the report gate's tenant map while the release goroutine
could still be undoing accounting after freeing its global slot. The failure said
"leak after cancel: map[]". The test now joins the full release and inspects the
map under its mutex; no report-gate production behavior or capacity was changed.
One hundred local race repetitions pass. A replacement needs its own full-server
and remote exact-head acceptance; the red run is not reclassified as passing.

### Staged gate live-broker subprocess acceptance (test-only)

The actual `MQTTGate` now has an opt-in isolated PostgreSQL and live Mosquitto
subprocess test, also reached by the existing local mTLS pipeline suite as the
separately named `StagedPlaintextProcessingGate` subtest. That name matters: this
fixture uses anonymous PLAINTEXT LOOPBACK only, a stable subscriber ID, manual ACK
and a live in-memory broker. It does not extend the pipeline's TLS/ACL proof to
staged processing, and creates no production subscriber, activation option or
all-handler configuration.

An existing packet observer records subscriber deliveries and actual forwarded
PUBACK packets. A synthetic database insert refusal produces neither retained
telemetry/work pair nor observed PUBACK. Killing and restarting that test
subscriber with the same persistent session redelivers and retains one pending
pair. A second input stops inside an ACK wrapper after the real gate commits but
before Paho Ack; SIGKILL and restart reconcile the same pair without duplicate
admission. A synthetic retained `outcome_unknown`/attempt ID and the ENTIRE
captured plan survive that replay even after a notification target changes.
An ordered unique publisher barrier confirms resumed delivery; no derived-flow
history is created. There are no provider sends or credentials.

Three local race repetitions pass. Initial harness failures used the base URL
instead of the child database (a connection config's original ConnString was not
rewritten), and queried a nonexistent flow_runs tenant column; the fixture now
passes the explicit isolated database URL and joins through flows. Only final
passes count. This is subscriber-process/live-broker recovery evidence, NOT broker
restart, unsaved broker input recovery, disk/fsync/power-loss, edge publisher
outbox/application receipt, production ACK/session policy, real provider or owner
readiness. Existing open runtime/unsaved-input findings and exact93 update guards
remain unchanged. The normal exact-head suite still must pass before acceptance.

### Staged gate direct certificate-required transport (test-only)

`StagedCertificateProcessingGate` adds a SEPARATE disposable mTLS broker variant
of the actual staged Engine/MQTTGate subscriber subprocess test. Broker config
requires certificates and certificate-CN usernames, uses generated `brokeracl`
entries, and the gateway/subscriber use production `mqttconfig.Options` to verify
the local server certificate. A missing-client-certificate handshake is actually
refused. The test-only ingest subscriber reads `t/#` under its service identity;
a gateway's foreign-topic publish has zero callback/admission before an ordered
authorized same-publisher telemetry barrier. The broad subscription makes that
ACL assertion meaningful rather than merely failing to subscribe to the target.

The direct TLS variant also exercises DB refusal, subscriber SIGKILL/restart,
commit-before-ACK interruption, one-pair exact replay with complete retained
plan/unknown attempt unchanged despite target edits, and zero derived history.
It connects directly, not through a TLS-terminating observer. Its callback and
ACK-invocation markers are NOT encrypted wire PUBACK evidence. Actual forwarded
wire PUBACK observation remains the separate plaintext variant's bounded proof.
Three local race repetitions of BOTH variants pass. The initial test helper
passed `1` instead of transport `true` for TLS; fixed before final passes.

These are synthetic certificates/loopback services and test-only subscriber
options, not a production processing startup mode, identity permission grant or
all-handler ACK/session policy. No broker restart/disk/fsync/power-loss,
unsaved-input recovery, publisher outbox/application receipt, provider delivery,
GUI/native installer/hardware or owner readiness is accepted. Runtime ACK/replay
and candidate unsaved broker-input findings, exact93 guards and staged94 image/
restore gates remain unchanged. A new remote exact-head suite must pass too.

### Receipt identity binding groundwork (default-off, in-process only)

The staged `AdmissionReceipt` now includes version1, policy `processing-v1`,
tenant/gateway/device/point/event IDs, original UTC microsecond observation time,
lowercase SHA256 of the ORIGINAL canonical envelope, retained state and new/replay
flag. The digest hashes canonical Envelope JSON in its declared field order,
including default optional fields, not raw MQTT bytes. A literal protocol vector
pins that representation; whitespace/key order, omitted topic identities and
same-instant offset timestamps normalize to the same digest. A changed canonical
input field produces a different digest. A future incompatible canonical encoding
needs a version decision rather than silently changing this representation.

Receipt construction uses the original retained envelope after real transaction
commit or exact replay, never mutable configuration/targets or unchecked wire
bytes. It refuses unexpected policy/state or noncanonical time and returns zero
on any admission error/conflict. State changes do not change input identity/digest
or authorize replaying effects. Complete route/identity/digest binding is a
prerequisite for a future edge outbox, not an implemented deletion protocol.

Three local PostgreSQL race repetitions cover receipt vectors, wire/gate refusal,
commit/replay, complete identity/digest binding, terminal retained states, changed
targets and conflict-zero receipt, plus no plan/target/message/attempt disclosure
or derived history. The local development checkout and services were restored
from the exact published review tip after an environment reset before this work;
no owner environment was restored or changed. Fresh full-server/vet and a new
remote exact-head run remain the acceptance gates.

NO receipt is published, no MQTT topic/ACL/permission is added, and no edge queue
item is matched or deleted. A digest is not a MAC/signature, source authentication
or evidence of MQTT/provider delivery. A future authenticated receipt receiver
must match the complete version/policy/route/identity/time/digest and establish its
sender before deleting anything. Existing broker-ACK-based queue behavior is
unchanged and is not called application admission. Production transport/all-handler
ACK/session, broker-disk/unsaved-input recovery, outbox, provider, native/hardware,
staged94 image/restore and owner-readiness gates remain open. Exact93 guards remain.

### Separate staged edge admission outbox foundation (unwired)

`edge/internal/admissionoutbox` is a new opt-in library, NOT the production
`queue.Queue` and not an owner migration. Disabled Open refuses before touching
its path. Enabled isolated tests use a separate absolute-path SQLite database,
WAL/FULL, a pinned schema/version/policy and persisted capacity. Legacy queue
schemas, altered schema/trigger/policy/version and capacity changes are refused,
not migrated. No existing queue code, publisher or runtime startup calls it.

Put retains caller-created stable event/time and complete canonical reading
before returning positive evidence. It refuses missing clocks, non-UTC or
submicrosecond time, non-finite values and invalid routes rather than inventing
an observation time or silently rounding identity. Its literal canonical JSON
vector matches the separately pinned server receipt vector. Exact duplicates
return the original sequence even at capacity; conflicting payload/route at the
same event/time is refused. A full store refuses the new input with no oldest-row
eviction. Failures and uncertain commits return zero evidence. Callers must
visibly surface failed retention; no runtime acquisition path does so yet.

Next returns an ordered bounded batch only after rechecking every stored row's
identity/time/topic/canonical payload/digest. Any bad row refuses the whole batch,
not silent loss. Pure typed MatchReceipt compares version/policy/full route/event/
time/digest/state, but is deliberately NON-DESTRUCTIVE. It is not a network JSON
receiver or source authentication. A forged receipt with the same public fields
can match, so there is NO deletion, delivered marker or acknowledgment API.
Neither broker PUBACK nor completed state changes storage. Authenticated receipt
transport and a reviewed atomic deletion/reconciliation protocol remain required.

Local tests cover disabled/no-path access, legacy database preservation, schema
refusal, canonical vector and invalid inputs, duplicate/conflict/capacity/reopen,
WAL/FULL settings, all receipt binding mutations, terminal-state non-deletion,
whole-batch corruption refusal, cancelled/closed operations, concurrent handles
never overfilling, and actual child-process SIGKILL after committed Put followed
by reopen/exact replay. That is process-restart evidence, NOT disk power-loss,
filesystem/hardware, broker/publisher transport, physical meter or owner proof.
Full edge/server and a new exact-head remote suite remain the publication gates.
The initial cold-cache local full-server command exceeded its time budget and is
NOT accepted; the fresh isolated final database run is the accepted local suite.
No topic/ACL/permission, worker/retry, provider effect, native installer, exact93
update behavior, owner activation or deployment changes are included. All-handler
manual ACK/persistent sessions, authenticated application receipt, runtime edge
outbox wiring and capacity UX, immutable staged94 restore/rollback and the joint
secure-server/fresh Windows installer acceptance remain open.

### Signed admission and exact staged reconciliation (unwired, opt-in only)

This next isolated path adds to the non-destructive foundation above. It does
NOT activate a publisher/receiver, migrate an owner database or change the
existing production broker-ACK queue. No startup handler uses either new API.

Engine.AdmitSignedMQTT requires BOTH the existing explicit enabled engine and
explicit enabled signing configuration. Bad audience/key/config refuses before
DB access. It signs only the existing committed/exact-replay AdmissionReceipt,
not arbitrary caller-provided receipt data, and returns no token on admission
failure/conflict or observed cancellation. Caller-authenticated publisher/topic
ACLs are still prerequisites. It does not publish or execute derived work.

The version1 signed wrapper uses Ed25519, a domain-separation prefix, SHA256
public-key identifier, exact deployment audience and the complete existing
receipt. Canonical JSON bytes are signed. Edge verification requires an explicitly
pinned public key, exact audience and tenant/gateway scope, not a key supplied
by the message or auto-discovered from the server. Exact re-encoding rejects
alternate/duplicate/unknown/case-folded keys, malformed/trailing/oversized input
and noncanonical encoding. Cryptographic validity alone never grants trust.
Ephemeral isolated tests generate keys; no runtime keys, token, permission,
enrollment, storage, rotation, revocation or trust/rollback policy is provisioned.
Those production pairing and key-management decisions remain acceptance gates.

Store.ReconcileSigned is a separate explicit opt-in staged API. It verifies the
signature/scope, rereads the caller-selected sequence inside a SQLite transaction,
checks full original route/event/time/canonical payload/digest against the signed
receipt, then deletes only that exact row and commits. Failed verification,
foreign/changed input, corrupt retained evidence, cancellation and failed DELETE
return false without positive deletion evidence. A commit error is uncertain,
not proof of rollback. Reconciliation must inspect the SAME stable identity;
never create a fresh event or retry external effects. Missing already-deleted
rows return ErrMissing, not a second positive acknowledgment. Existing pure
MatchReceipt remains non-destructive. Broker PUBACK/unsigned receipts cannot
invoke a trusted deletion path. Terminal receipt states mean retained admission
only, never notification execution, provider delivery or recipient read.

Isolated tests exercise an actual server PostgreSQL admission/signature whose
public evidence is passed to an independent edge Go test, retained in its staged
SQLite outbox, verified and atomically deleted. The bridge contains only a
synthetic reading, public key and signed evidence, never the ephemeral private
key. It also covers failed admission/conflict producing no token, replay after
configuration/state changes, all trust/binding mutations, wrong key/domain,
properly signed but noncanonical/foreign evidence, duplicate/unknown/trailing
encodings, failed DELETE rollback, corrupted retained row, cancelled operation,
concurrent reconciliation with one positive result, exact missing-row handling,
and actual child SIGKILL after committed deletion followed by reopen. These are
in-process/cross-module and process-restart tests, NOT network receipt delivery,
disk power-loss or owner/hardware evidence. The first local compile attempt
caught a test import/helper name collision; it was fixed before passing tests.

No network endpoint, receipt MQTT topic/subscription/ACL, signer key provisioning,
publisher retry policy or runtime edge reconciliation caller is added. Full fresh
server/edge/vet and exact-head remote tests remain required before accepting this
slice. All-handler manual ACK/session/shutdown, authenticated runtime pairing and
transport/key lifecycle, capacity UX, derived stages/per-effect receipts,
staged94 immutable-image restore/rollback and the joint secure-server/fresh
Windows installer/hardware acceptance remain open. Exact93 guards are unchanged.

### Bounded staged publisher adapter (unwired, no transport implementation)

The explicit opt-in admissionoutbox.Publisher now connects the separate retained
input store, a one-call Exchange contract, pinned receipt verification and atomic
reconciliation. It is NOT a daemon or runtime switch. No production caller or
network Exchange implementation exists; mqttc.Publish/PUBACK alone cannot satisfy
its requirement for signed SERVER ADMISSION evidence.

RunOnce snapshots at most1-256 rows, revalidates the whole batch and preflights
its complete tenant/gateway scope before the FIRST send. It invokes Exchange
sequentially at most once per selected row, using a copied payload and frozen
trust pin. Errors, missing/bad receipts, observed cancellation or uncertain local
commit stop the batch before the next row. Only successfully pinned, fully bound
signed evidence permits the existing exact-row transactional deletion. Selected,
Attempted, Reconciled and StoppedSequence report partial progress without calling
remaining snapshot size a live queue depth. Reconciled means admission ONLY,
never derived effects/provider delivery.

There is no internal retry, reconnect, redirect, identity/time substitution,
parallel send or automatic next batch. A caller must explicitly invoke a later
SAME-input reconciliation after an uncertain result. Close permanently refuses
new batches and waits for the current batch including reconciliation; timeout
stays closed, not proof of cancellation or rollback. Panic releases the local
batch gate but does not convert uncertainty into success. Exchange must honor
context and perform exactly one operation without retries/fanout. That contract
is not a tested network implementation. Dedicated store/Exchange ownership is
required during a run; the gate protects this instance only, NOT other processes
or publisher instances. Cross-process ownership/lease and crash-mid-network
coordination remain runtime acceptance gates.

Isolated tests cover disabled/invalid/typed-nil config, frozen trust pin,
whole-batch scope/corruption refusal before any send, empty batches, batch bounds,
partial success then failure without sending a third row, broker-only ACK,
unsigned/wrong/tampered receipts, send error/cancel/panic retention, concurrent
invocation refusal, Close timeout/drain, transport payload mutation isolation,
store reopen retaining the exact same sequence/payload and later explicit signed
reconciliation. The existing real PostgreSQL server-to-independent-edge fixture
now also supplies actual committed and terminal-replay signed evidence through
this publisher contract: a synthetic first exchange loses the response, retains
the input, and a second EXPLICIT call reconciles the same input. No network
exchange occurs in that fixture. Both verbose bridge markers are local evidence;
normal full-package CI does not separately print passing subtest logs. Initial
local frozen-pin test failure was a signing-fixture key-ID mutation, fixed before
passing repetitions. No production key provisioning, transport/topic/ACL,
publisher worker, original queue switch, owner migration, native installer or
exact93 behavior change is included. Full fresh server/edge/vet and a new exact-
head remote suite remain the acceptance gates. Runtime authenticated pairing/key
lifecycle, one-attempt network transport, all-handler persistent ACK/session,
capacity UX, derived stages/per-effect receipts, staged94 images/restore and the
joint secure-server/fresh Windows installer/hardware outcome remain OPEN.

### Explicit one-attempt HTTPS Exchange (unwired, loopback mTLS tests)

The separate admissionoutbox.HTTPSExchange now implements the staged Exchange
contract behind explicit enabled configuration. It requires exact HTTPS endpoint
without URL credentials/query/fragment, supplied trust roots, matching client
certificate/private key and a1-30second whole-operation timeout. The roots and
certificate bytes are detached from caller configuration; private-key objects
must not be mutated concurrently. TLS validates the endpoint hostname and CA;
the server must request/verify the client certificate. No runtime caller, endpoint
registration, key enrollment/provisioning, owner setting or production queue
switch is added. TLS is NOT a replacement for the independent signed receipt pin.

AdmitOnce preflights the full canonical retained item before network access,
sends one HTTP/1.1 POST with the exact retained payload and derived topic header,
and uses a fresh connection/Transport per invocation. POST has no GetBody or
idempotency header, redirects are refused, environment proxies are ignored,
keepalive/HTTP2 are disabled, and there is no application retry or automatic next
request. Standard DNS/TCP address selection remains the normal dialer, not a claim
of one IP lookup or TCP packet. Request/response header/body/whole-operation
bounds apply; non200, nonJSON, encoded, truncated, empty or over8192byte responses
fail without positive evidence. Automatic compression is disabled. Any error can
follow a real server commit and MUST retain the SAME input for explicit later
reconciliation. Returned bytes still require the existing pinned signature/full
identity/atomic deletion path; a successful TLS/HTTP response alone cannot delete.

Disposable loopback tests cover client-certificate-required handshake, wrong CA,
wrong hostname SAN and absent client certificate refusal before any HTTP handler,
exact topic/body/HTTP1.1/close semantics, inherited bad proxies not used, no redirect
or503retry, empty/oversize/encoded/nonJSON/unsigned/truncated responses, abrupt
response loss, cancellation, preflight refusal before network and bounded timeout.
The real PostgreSQL server-to-independent-edge fixture additionally forwards
ACTUAL committed and terminal-replay server signatures through this mTLS network
leg: the first handler abruptly loses the response, exactly one POST occurs and
the row remains; a second EXPLICIT publisher invocation sends the same input,
verifies the actual replay evidence and atomically deletes it. Its HTTP handler
only forwards precomputed PostgreSQL-backed evidence, not live transactional
admission at request time. It is NOT a production authenticated admission handler,
server authorization/pairing or end-to-end owner integration. Local verbose marker
SERVER_SIGNED_MTLS_EXPLICIT_REPLAY_OK is separate from normal package-CI output.

The first cold-cache transport-test command exceeded its time budget and is NOT
accepted. A bounded verbose run and final repetitions are the accepted tests;
fixture cancellation cleanup was bounded and wrong-host certificate installed
before listener start. Full fresh server/edge/vet and exact-head remote acceptance
remain required. Runtime server handler/client identity authorization, deployment
endpoint/key lifecycle, cross-process publisher ownership, all-handler ACK/session,
capacity UX, derived stages/per-effect receipts, staged94 images/restore and joint
secure-server/fresh Windows installer/hardware acceptance remain OPEN. Exact93,
production queue and owner state are unchanged; no new token/permission or physical
write is included.

### Certificate-bound real admission HTTP handler (unwired)

`ingestprocessing.AdmissionHTTP` is an explicitly constructed, default-off
handler, not a registered API route, listener or owner deployment. It freezes
an explicit administrator-supplied full leaf-certificate SHA256 -> exact
telemetry topic map (1-256 entries, no duplicate certificates), a signing key
copy and deployment audience. It requires actual request TLS handshake evidence
with a verified chain matching the peer leaf, then checks the exact certificate
pin, topic header and payload route before invoking the real transactional
`AdmitSignedMQTT`. Common names and proxy authentication headers grant nothing.
This is a static isolated binding, NOT pairing, revocation, key enrollment or
rotation. A future dedicated server must require verified client certificates,
protect keys and configure header/idle timeouts and header size limits. Do not
mount behind TLS termination or treat a proxy header as request TLS evidence.

The handler accepts only HTTP/1.1 POST at the fixed staged path, with no query or
encoded alternate path, exactly one JSON content type and exact topic header,
no content encoding and at most 8192 input bytes. It sets actual connection
read/write deadlines (1-30 seconds) and a transaction deadline; wrappers without
deadline support refuse. It returns only the actual committed/exact-replay
signed receipt, with no-store/connection-close. Any database, deadline, short
write or connection-loss outcome is uncertain, not rollback. No retry, derived
execution, provider send, MQTT ACK or existing production queue change occurs.

New isolated acceptance invokes this actual handler over disposable loopback
mTLS, real PostgreSQL admission and the independent edge retained store,
HTTPSExchange, Publisher, pinned signature verification and atomic deletion.
The first response is dropped AFTER real handler admission/signing. Edge keeps
the exact row. Only a second explicit RunOnce sends the SAME input; actual
request-time replay signs the original committed plan and edge deletes only
after verification. The server asserts exactly two requests, one telemetry and
processing row and zero started effects. Unlike the earlier forwarding fixture,
this test does NOT forward precomputed receipt bytes. Marker:
`LIVE_HANDLER_PG_MTLS_EDGE_REPLAY_OK`. A conflicting input gets no signature.
Other tests cover invalid configuration, copied key/routes, foreign same-CA
client, absent client certificate, unverified/mismatched chain, proxy spoof,
foreign or duplicate topic, malformed/cross-tenant/oversized body, duplicate
content type and unsupported method/path/query/encoding before database access.
Actual connection-deadline tests refuse a slow incomplete body; unsupported
deadline wrappers fail closed. A PostgreSQL insert trigger refusal yields no
signature and neither telemetry nor processing row for that new input.

This is still isolated handler acceptance, not an enabled production service or
end-to-end owner readiness. Runtime listener/pairing/key lifecycle and dynamic
revocation, cross-process publisher ownership, capacity UX, all-handler
ACK/session/shutdown, derived work replay, staged image/restore compatibility,
GUI/UAC/reboot/physical meter checks and combined server/fresh EXE handoff remain
open. Existing exact93 update guards and native evidence scope are unchanged.

### Explicit publisher ownership across cooperating local processes

The unwired Publisher now acquires a dedicated adjacent SQLite ownership guard
before taking its input snapshot or invoking Exchange. `BEGIN IMMEDIATE` with
zero busy timeout stays held through the entire explicit batch, including a
blocked exchange and signed reconciliation. Another instance/process refuses
before Exchange instead of waiting to perform a later send. Cancellation or a
Close timeout cannot release a still-running exchange's ownership. Release runs
with its own bounded context after the batch returns. Release failure is reported
as uncertainty, never converted into permission to send again.

The guard file is `<canonical-input-path>.publisher-owner.sqlite`, with an exact
versioned single-table schema. Existing foreign/altered/extra-row/empty-row guard
schemas refuse, not migrate or replace. It is separate from the retained input
database, so collecting new retained rows does not wait on a network request.
The input schema/capacity/policy and production queue remain unchanged. The
in-process Publisher gate and permanent Close/drain semantics still apply.
Existing path and parent symlinks are canonicalized so those aliases converge.
The guard itself must be a regular file, not a symlink or special file.

This is coordination between cooperating processes using the SAME canonical
local input database. Both files must remain in a trusted local directory. Do
not hardlink, rename, copy, replace, delete or independently restore either while
publishing. Network filesystems, hostile local directory mutation, hardlink
aliases, independent copies and distributed ownership are NOT covered. The
guard does not authenticate a process, confer tenant authority or stop arbitrary
code that bypasses Publisher and calls Exchange directly. Runtime packaging
must preserve this single-path/private-directory contract and still coordinate
collection, pairing, source changes and lifecycle. No startup uses it yet.

Five local race repetitions cover two Store/Publisher instances, refusal before
send, input collection during a blocked exchange, Close timeout retaining the
guard, explicit later same-input reconciliation, canonical parent aliases and
malformed/symlink/cancel refusal. An actual child process holds ownership inside
Exchange while its parent is refused. The child is killed, its original retained
payload is checked, and only a later explicit parent RunOnce reconciles it.
Marker: `PUBLISHER_PROCESS_KILL_OWNER_RELEASE_SAME_INPUT_OK`. There is no PID-file
lease, clock expiry, timed takeover or automatic restart/resend. Process death
releases OS-managed SQLite locks; it does NOT roll back a remote admission,
remove the retained row or prove power-loss/physical-disk durability. The process
test is local Linux execution, not Windows native or network-filesystem proof.

Enabled runtime/pairing/key lifecycle, capacity UX, all-handler ACK/session,
derived work replay, staged image/restore compatibility, GUI/UAC/reboot/physical
meter checks and the combined secure-server/fresh EXE handoff remain open.

### Explicit scoped edge admission session (unwired composition)

`admissionoutbox.Session` now joins the retained Store, real HTTPSExchange,
pinned Publisher, local ownership guard and permanent Close/drain lifecycle.
OpenSession requires explicit outer AND component opt-ins, stable tenant/gateway
scope, deployment receipt pin/audience, TLS roots/client certificate, exact
endpoint and bounded batch configuration. Invalid transport/trust configuration
refuses before opening a file. No startup/collector/GUI route opens it yet; this
is not pairing, enrollment, a trust grant or an owner enable switch.

Retain accepts only the configured tenant/gateway, retains the caller's SAME
stable canonical event/time and returns committed evidence or explicit failure.
It never sends. SendOnce performs only the existing explicit bounded batch and
never schedules another one. Status exposes current session open/closing/closed,
its own send-in-progress flag, instantaneous retained/capacity/available/full
counts and a COPY of the last explicit batch result/outcome. Full refuses NEW
input without eviction; exact retained duplicates can still succeed. Count is
not a corruption/receipt health check, provider delivery estimate or promise of
space after the snapshot. No payload, endpoint, key or filesystem path is in the
status shape. Another process's active send is not this session's sending flag.

LastSend is process-local and absent after reopening, not a durable audit log.
Its outcomes distinguish idle, admission-confirmed, refused before an attempt,
and uncertain after an attempt. Batch counts preserve positive reconciliations
without implying effects completed. Another overlapping call refuses as busy
without overwriting the in-flight result. A failed/uncertain call is NOT rollback
or authority to retry effects. Only a later explicit SAME-input reconciliation
may follow. UI/persistent operational history and authenticated configuration
changes remain separate work, not silently inferred from these status fields.

Close permanently refuses new collection/send operations, drains outstanding
collection/status/send operations, then closes the retained DB. A timeout leaves
it closing with the active publisher/ownership intact. Status still reports
closing and current send activity but no stale queue count. A later explicit
Close can finish draining. Closing never deletes keys/config/input or rolls back
a possible server commit. Reopening preserves retained input and does not send.

The real PG/mTLS/live-handler bridge now drives the public Session composition:
Retain -> full capacity -> first explicit SendOnce loses the response AFTER real
commit/sign -> uncertain status/retained exact row -> second explicit SendOnce
gets real replay -> verified atomic reconcile -> empty capacity -> closed with
no current count. Server checks exactly two requests and one original admission,
with zero started effects. Required marker `LIVE_SESSION_PG_MTLS_EDGE_REPLAY_OK`
adds to the existing live handler marker. Tests also cover no-file config refusal,
foreign-route/conflict/capacity failure, exact duplicate at capacity, copied last
status, busy concurrency, Close timeout holding ownership and reopen retention.
Five full local outbox race repetitions pass. This is isolated composition, NOT
GUI, enabled owner runtime, new Windows native or physical meter acceptance.
Runtime authenticated pairing/endpoint/key lifecycle, durable status UX,
all-handler ACK/session/shutdown, derived work replay, staged image/restore
compatibility and the joint secure-server/fresh EXE handoff remain open.

### Explicit read-to-retain collector (unwired, no polling daemon)

The separate opt-in Collector now joins a caller-owned PollReader to the scoped
Session. No startup/legacy supervisor constructs it. PollOnce invokes only Poll,
not source writes, rules, actuators, discovery, send or source Close. Reader
ownership and an appropriate read-only source configuration remain the caller's
responsibility; a narrow interface does not authenticate arbitrary supplied code.
The existing production supervisor, broker-ACK queue and local rules are unchanged.

One call requires a complete sample of the exact configured device/point/unit
set (1-256 points), with no duplicate, missing, foreign or nonfinite values.
Partial poll/error/cancellation refuses the WHOLE sample before retaining any
of it. A complete sample gets unique event IDs once and one gateway post-poll
UTC microsecond observation time. This is NOT the meter clock, synchronized
clock proof or an invented timestamp on replay. Frozen readings are now retained in ONE SQLite transaction as described below,
not a saved prefix. Any failed/uncertain batch retain leaves the SAME whole
sample unresolved in memory. A new PollOnce refuses before a source
read while that batch is pending. Only explicit RetainPending retries retention
of those exact identities, without polling, sending or generating new events.

This is deliberately NOT a no-loss claim before durable retention: the pending
batch is volatile and process death before confirmed Put can lose it. Status
makes pending count and retention-unresolved distinct from confirmed retained
count; it exposes no raw readings, source address or private key. Capacity full
never evicts. A committed-but-uncertain exact duplicate resolves through the
existing duplicate Put path. Durable collector intent/spooling and UI warnings
remain required before enabled runtime acceptance. There is no automatic retry
or background polling cadence in this adapter.

Stop permanently prevents new reads and drains an active call; timeout stays
stopping and does not pretend a blocked reader finished. It never closes source
or Session, clears pending or sends. Explicit RetainPending is allowed after Stop
because it does not touch the source. Caller must inspect unresolved pending and
drain Stop before replacing/closing source; then close Session separately. Poll
must honor context or have bounded IO. The tested existing Modbus TCP driver has
its own IO deadlines, not instant context-driven interrupt of each socket read.

Five local collector race repeats and five full outbox race repeats pass:
invalid/typed-nil/no-file config, complete-sample refusal, whole-sample capacity refusal,
no second poll while pending, exact pending identity/time, explicit retention
after Stop, duplicate-commit reconciliation, busy/Stop timeout/drain and closed
Session before source read. An actual disposable Modbus TCP fixture observes
only function3/4 reads, then verifies Session retention and exact reopen hashes.
Marker `ACTUAL_READ_ONLY_TCP_COLLECTOR_SESSION_REOPEN_OK`.

The real server PG/mTLS bridge now starts with an actual disposable function3
Modbus TCP sample -> Collector -> Session Retain -> first explicit SendOnce ->
actual server commit/sign -> deliberately lost response -> retained original ->
second explicit SAME-input request -> actual replay signature -> pinned atomic
delete. Server checks two requests, one original telemetry/processing row and
zero started effects using the exact collected evidence, not a prefilled event.
Required marker `LIVE_COLLECTOR_SESSION_PG_MTLS_EDGE_REPLAY_OK` joins the earlier
session/handler markers. Three local race repetitions pass. These are synthetic
loopback sources, not physical USB/meter/model/decoder/clock or owner proof.
All runtime authenticated pairing/key lifecycle/GUI/durable operational UX,
production ACK/derived replay, staged image/restore and joint handoff gates stay
open. No new runtime activation, owner update, schema or physical write occurs.


### Atomic complete-sample retention (unwired)

Collector now uses Session.RetainBatch -> Store.PutBatch, one bounded 1-256 row
transaction on the SAME unchanged processing-v1 SQLite schema. Session checks
every route before any store access. Store preflights all canonical identities,
rejects repeated identities within a request, compares retained duplicates by
exact topic/payload/hash, and checks capacity for only genuinely new rows. All
new rows commit together or return zero saved evidence. No prefix is reported
as retained on a later conflict, full capacity, insert failure or cancellation.
An uncertain commit still requires explicit retry of the SAME frozen whole
sample, never fresh IDs or a new source read. Existing exact duplicates do not
use new capacity. Single-row Put and older compatible readers remain unchanged.
Collector refuses construction when configured sample size exceeds the store's
total capacity, before any poll. No new schema, migration, startup, daemon,
external effect, send or automatic retry is introduced.

Five batch/collector race repetitions and five complete outbox race repetitions
pass. Tests cover invalid last row, duplicate identities, conflicting last row,
full capacity, failure at the second actual INSERT after the first INSERT,
rollback/reopen with no prefix, existing-plus-new at exact capacity, exact whole
batch duplicate at full capacity, foreign route refusal before retain, competing
local Store connections, and original single-row API reads of committed batches.
A child process exits without closing SQLite both after an uncommitted prefix
and after a complete batch commit; reopening sees zero or the complete batch,
respectively, and explicit same-batch retry preserves IDs/hashes/sequence.
Marker ATOMIC_BATCH_PROCESS_EXIT_REOPEN_OK. The uncommitted-prefix child stages
SQL directly to test SQLite process recovery, not a forced crash inside PutBatch.
Actual disposable TCP function3/4 collector retention/reopen and the real
Collector -> Session -> mTLS/PG lost-response explicit replay bridge still pass,
including three local race bridge repetitions.

This closes partial sample capacity/insert retention, NOT the interval between
source read and durable commit. Pending remains VOLATILE and not a durable retry
intention; process death before confirmed retention can lose the sample. No host
power-loss, physical meter, native Windows runtime, GUI operational recovery or
production no-loss acceptance is claimed. Durable collector intent/operator UX,
authenticated pairing/key lifecycle, production ACK/derived work replay, staged
image/restore compatibility and the joint server/fresh EXE handoff stay open.


### Opt-in read-only local recovery panel (no runtime activation)

Collector.RecoveryStatus samples the real Collector and Session without polling,
retaining, sending, reconciling or closing. It exposes bounded status/counts and
process-local last-send summary, not payloads, event IDs, source addresses,
endpoints, private paths or keys. Failed/cancelled reads return zero evidence.
The two objects are sampled separately, NOT an atomic snapshot or delivery
receipt. Observation time is the gateway clock, not meter-clock proof.

Local UI Info.StagedAdmission is a separately supplied opt-in callback. Existing
production callers leave it nil: the panel stays hidden and makes NO staged
status fetch or cadence; /api/admission-status returns404. Only GET/HEAD is
allowed, writes refuse before callback invocation. Opted-in probes get a one
second context deadline; callbacks must be cheap/cancellable and read-only. A
misbehaving callback cannot be forcibly interrupted by this context. Failures
return503 generic text without partial counts or error strings. Existing no-store,
CSP and content-sniffing/referrer headers apply. There is no network listener,
new authentication grant, startup registration or operational action here.

The read-only panel separates in-memory pending from saved rows, warns against
new reads/source replacement while pending, marks full capacity without eviction,
and distinguishes open/stopping/stopped and closing/closed session. It never
shows a current queue count after session closure starts. A last send is clearly
process-local; uncertain server commit needs SAME input explicit reconciliation,
not a fresh event or automatic retry. Signed admission is NOT rules/commands or
provider completion, and this store is separate from the legacy broker buffer.
The panel cannot recover or retry pending input. Pending remains VOLATILE before
confirmed commit; keeping the process open matters. It is not a durable journal.

Enabled local status reads run every5seconds with no overlapping fetch and a
3second browser timeout. Unavailable/malformed status clears prior counts and
observation time rather than showing stale evidence. This is a local loopback
preview cadence, not polling another person's logged-in external account.
Actual Chrome desktop1280/mobile390 renders were pixel-inspected for pending,
uncertain, closed and unavailable states, including after clearing prior data.
They use synthetic fixture JSON and are render/behavior evidence, not owner or
native runtime proof. Nine JavaScript regressions run the actual embedded HTML
functions in DOM tests for default-hidden/no-fetch, pending, all four last-send
outcomes, closed/no-stale-count, failure/malformed clearing, timeout and overlap.
Real Collector/Session capacity-failure and stopped/closed objects, no-secret
JSON, and handler opt-in/read-only/deadline/redaction pass three race repeats.
No start/stop/send/retry button, configured runtime callback, native installer
acceptance or hardware proof is added. Durable intent/operator actions,
authenticated endpoint/pairing/key lifecycle, production ACK/derived replay,
staged image/restore and the joint server/fresh EXE handoff remain open.


### Separate explicit collection journal (unwired, no migration)

NewJournalCollector is a second explicit opt-in constructor; ordinary
NewCollector stays memory-only. It opens a NEW separate SQLite journal, never
migrating/importing the retained or legacy queue. A strict two-table schema holds
one whole pending sample plus version and scope fingerprint. Scope fixes tenant,
gateway, device, exact point/unit map and canonical retained-store path. WAL and
FULL synchronous are required. Payload has a bounded length, exact canonical
bytes and SHA256, and every recovered reading must match the configured complete
sample, common original observation time, identities and measured quality.
Hashing detects accidental corruption, NOT a hostile local writer who can
replace both data and digest. This is not encrypted storage or source authenticity.

PollOnce freezes event IDs/time once, commits the WHOLE journal sample BEFORE
RetainBatch, and clears the journal only after a positive retained-store commit.
Full capacity leaves the exact journal sample intact. A failed/uncertain journal
save starts NO retention and reports journal-unconfirmed. A failed/uncertain
clear leaves exact recovery pending; retained rows may already exist. The next
explicit RetainPending saves/compares the SAME journal bytes, retains the SAME
identities idempotently and clears only matching journal content. No replacement,
eviction, automatic poll, send or retry exists. If another explicit send already
reconciled/deleted those rows, an explicit recovery can reinsert the same IDs;
the existing server admission dedup/replay contract still applies. Do not treat
pending and retained counts as disjoint or add them after an uncertain clear.

Opening/reopening only validates/loads the sample; it NEVER polls, retains or
sends. A new poll refuses until explicit recovery resolves pending. Dedicated
SQLite ownership is held across the journal collector lifetime, including Stop.
Close prevents ALL new collector calls, drains active work then closes/releases;
timeout keeps ownership until a later successful Close. Stop still permits
explicit retention, does not close caller-owned source/Session and does not
release journal ownership. Cooperating local processes using the same canonical
path and intact adjacent ownership sidecar exclude one another. Keep all journal,
SQLite WAL/SHM and ownership files in a trusted LOCAL directory. No networkfs,
hardlink/copy/rename/hostile-directory/distributed guarantee, no owner file move,
no upgrade of existing databases, and no native Windows claim is made.

Three complete local outbox race repetitions pass, including child process exit
WITHOUT cleanup after actual Collector PollOnce journal commit/capacity failure.
Reopen gets the exact original pending IDs/time/hash, zero new source polls or
sends, then explicit retention reuses those inputs. Marker
ACTUAL_COLLECTOR_JOURNAL_PROCESS_EXIT_EXACT_RECOVERY_OK. Save refusal causes no
retained rows/false durability; clear refusal preserves exact rows and explicit
retry/reopen preserves sequence; scope/schema/hash/payload mismatches refuse;
second cooperating owner and Close-timeout takeover refuse before a new read.
Stop keeps ownership; confirmed Close releases it. These are local process-exit
and close/reopen tests, NOT a host power cut or native/hardware acceptance.

The actual live bridge now reads disposable Modbus TCP function3 -> commits
journal -> actual retained INSERT fault -> closes/reopens collector and Session
-> explicit exact journal recovery with a reader that fails if polled -> real
mTLS/PG commit and lost response -> second explicit signed replay -> atomic
retained deletion. Three local race repetitions pass, two server requests, one
original admitted row and zero effects. The server test requires the new
LIVE_JOURNALED_COLLECTOR_PG_MTLS_EDGE_REPLAY_OK marker alongside existing markers.
No owner endpoint/pairing/key enrollment or runtime startup is wired.

Status adds pending_durability: none, memory, journal-unconfirmed or
journal-committed. The read-only panel distinguishes confirmed journal pending
from potentially volatile pending, warns to keep journal/ownership files intact,
labels overlapping recovery/retained counts, and still has NO recovery action.
Ten embedded-HTML JavaScript tests cover those branches. Desktop/mobile Chrome
pixels are synthetic render evidence only. This narrows the previously open
post-journal-commit restart gap, NOT the interval from source read to first
confirmed journal commit. That interval can STILL lose a reading. SQLite/device
power-loss semantics, full operational recovery UX, runtime authenticated
pairing/key lifecycle, production ACK/derived replay, staged image/restore and
joint secure-server/fresh EXE handoff remain open. No blanket no-loss claim.
