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
