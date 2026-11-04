# Multi-node high availability (3 nodes)

Single node: `scripts/install.sh` (already built, unchanged, see docs/install.md). Multi node: `scripts/install-ha.sh` (this page). One entry point offers both: `bash scripts/setup.sh` shows a menu (1 single node, 2 multi node), or `bash scripts/setup.sh single|multi [args]`. The menu itself is tested only for the non-interactive paths; the interactive prompt has not been exercised on a terminal.

**Status, stated plainly.** The generator is tested (`scripts/test-install-ha.sh`: input validation, rendering, YAML structure, secrets handling, file modes). The generated HTTP routing has also been exercised with a real local HAProxy 3.2.26 binary and three synthetic HTTP backends (`scripts/test-ha-http-routing.py`). This is not a rendered-stack test. The rendered stack (etcd, Patroni, HAProxy, keepalived, the Patroni image) has **never been started**: there is no Docker and no second machine in the build environment. Every failover claim below is a design, not a result. The drill at the end must pass on real hardware before you rely on it. The generator's own test caught two real bugs in the rendered files (an unquoted etcd host list and an invalid volume list), which is a sign the rendered files likely still hold more.

## What you get

Three nodes and one virtual IP (VIP). Every node runs the same set:

| Part | Mode | Failover behaviour (design) |
|---|---|---|
| etcd | 3-member cluster | Holds the "who is the Postgres leader" lease. Needs 2 of 3 up. |
| Patroni + Postgres 16 | 1 leader, 2 replicas, synchronous replication | Leader lease expires (about 30 s), a replica that has all committed data is promoted, the old leader is rewound with pg_rewind when it returns. |
| HAProxy | on every node | Port 5000 selects a node passing Patroni primary checks; check lag and existing sessions are not fencing. Web 8080 and API 8000 are load-balanced across all nodes (active-active). MQTT 1883 goes to one broker, the others are backups (active-passive). |
| keepalived | VIP floats between nodes | The VIP moves to another node if HAProxy or the node dies. |
| api, ingest, mcp, web | active-active on all 3 | Stateless. Ingest replicas share the MQTT load with shared subscriptions (`INGEST_SHARED_GROUP`). The scheduler runs on one replica at a time (Postgres advisory lock). |
| OAuth state | shared Postgres, `OAUTH_STATE_BACKEND=postgres` on every API | Login and Google-connect callbacks use atomic single-use state shared across API replicas. Database outage fails closed. Existing tracked bearer sessions also live in Postgres. |
| Response cache | off by default (`CACHE_BACKEND=off`) | Every request reads its backend. No node-local response entry can survive a write on another API. This costs backend load; it is not a cross-query snapshot guarantee. Independent unused Redis services are no longer rendered. |
| mosquitto | one per node, session files not replicated | See the MQTT limits below. |

Clients use: web `http://VIP:8080`, API `http://VIP:8000`, MQTT `VIP:1883`, Postgres `VIP:5000`.

## HTTP routing checks and remaining state-store gap

The web and API HAProxy listeners now require an exact HTTP 200 from
`GET /readyz`, not merely an open TCP port. Web nginx forwards that path to
its local API; API readiness checks Postgres and, when the selected OAuth state backend is Redis,
a PING to that store (including AUTH). With PostgreSQL OAuth state and an optional
Redis response cache, cache failure does not remove an otherwise ready API; shared read errors bypass
response caching and read the API backend directly. A database-unhealthy API or broken
web-to-API proxy therefore fails this check even while its port is listening.
Checks run every 3 seconds, with 3 failures to remove and 2 successes to restore
a backend. Requests can still fail during detection or an in-flight outage.
A failed dependency returns only `not ready` with HTTP 503, never raw errors,
addresses or secrets. The complete check has a two-second deadline and is not
cacheable. `/healthz` retains its existing database-only behavior. The state-store
PING proves connectivity/authentication, not writable capacity, state replication,
or failover safety. Readiness does **not** establish MQTT, OIDC provider or scheduler
health. The listeners stay in TCP mode; this change does not add HTTP retry or
replay writes on a different node. TLS termination and client-address forwarding
across the full chain remain unverified.

Local reproduction (2026-10-30): the old generated configuration continued
round-robin requests through all three open-port backends while their health
endpoints returned 200, 503 and 302. With the new generated checks, both web and
API listeners exclude 503/302/500 backends, restore a recovered backend to
round-robin, refuse successful responses when all are down, and recover after
that condition. A POST whose application response is 503 is delivered once,
not replayed. The test rewrites only bind/backend addresses to loopback, keeping
the rendered check paths, expected status and timing. Run:

```
bash scripts/test-install-ha.sh
HAPROXY_BINARY=/path/to/haproxy python3 scripts/test-ha-http-routing.py
```

The binary was built from official HAProxy 3.2.26 source with its published
SHA-256 verified. The generator now pins `haproxy:3.2.26-alpine`, replacing the
end-of-life 2.9 branch. The vendor lists 3.2 as LTS through 2030-Q2; the binary
reports that supported status. A real-container regression remains required
before deployment. This local binary result does not verify that image,
Docker networking, VIP movement or a real cluster. A separate local nginx 1.18
exercise (`scripts/test-nginx-readiness.py`) confirms the real web config forwards
`/readyz` and its query string, preserves API 503 and `Cache-Control: no-store`,
recovers to 200, and leaves unrelated SPA routes intact. That is not a test of
the production nginx image or of the complete cross-node chain.

API regression tests cover database failure, shared-state failure, AUTH rejection,
unexpected PING response, generic error output and a real local TCP connection
that accepts a command but never answers (bounded at two seconds). Readiness
checks do not retry or send writes. The full Go suite ran against a fresh local
Postgres 14.24 database; vet/build and all 306 web tests/build passed. An unrelated
web test clock collision was fixed by freezing Date: a whole-page assertion for
an old chart value of `42` could accidentally match the current response timestamp.
The production UI is unchanged by that test-only fix.

**Valkey is not generated as an HA service yet.** The optional single-node profile still uses Redis; the HA generator no longer starts unused independent Redis services. The direct client now supports verified TLS and ACL authentication
to an explicitly supplied Redis/Valkey endpoint; that does not create a replicated service. Valkey's vendor documentation states RESP2/RESP3 and
Redis OSS 7.2 compatibility. HexThings uses AUTH, GET, SET with EX/NX, GETDEL and
INCR through a small direct TCP client. Actual Valkey 9.0.7 TLS/ACL command tests now pass locally; this is not a migration
or failover test. The client has no Sentinel discovery or Cluster MOVED/ASK handling. A proxy layer would not by itself replicate independent
stores or make OAuth state available on every API. The generated APIs no longer route OAuth state or response-cache invalidation
through those independent stores. A shared, secured Valkey endpoint and its
failover/persistence policy still need design and verification; no Valkey proxy,
cache HA or RPO/RTO promise is made here.

### Opt-in generated external shared cache with mutual TLS

Default rendering uses Postgres OAuth state and disables response caching (`CACHE_BACKEND=off`).
To wire all three API replicas to one already managed Redis/Valkey TLS endpoint:

```
bash scripts/install-ha.sh render --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10 --out ha-out --shared-cache-mtls
```

On every node, set `SHARED_CACHE_AUTHORITY` in its private `cache.env` to the same
URL-encoded `user:password@host:port/database` value, without a scheme. The generated
URL always starts with `rediss://`; plaintext cannot be selected by this option.
Start using `docker compose --env-file .env --env-file cache.env up -d --build`.
`cache.env` is mode 600, is used only for Compose interpolation, and is not a
container `env_file`. Only the API receives the assembled URL; the authority is
not added to the common `.env` inherited by other services. Keep both files private
and do not publish expanded `docker compose config` output, which contains secrets.
Use a hostname covered by the server certificate. Install `ca.crt`, `client.crt`
and `client.key` in each generated `cache-tls/` directory. They must be real operator
supplied materials; the generator creates no identity, password or certificate.
Keep each private key mode 600 and directory 700. A missing authority stops Compose
interpolation; a missing directory is not auto-created by the bind mount, and
missing, malformed or mismatched TLS files stop API startup. Only API receives
this read-only mount. Runtime user permissions must allow API to read the files.

The generated API environment explicitly selects `CACHE_BACKEND=redis` with the
same endpoint/database for every replica, while `OAUTH_STATE_BACKEND=postgres`
stays unchanged. This adapter works with tested Valkey RESP commands too. No node-local Redis service, Redis data volume or unused Redis password is rendered in either mode. Existing generated directories do not change automatically. Do not delete old volumes as part of this update; inspect ownership and contents first.
TLS renewal/reload needs a deliberate API restart and coordinated file replacement;
no certificate rotation automation is added. The endpoint's ACL must allow
`GET`, `SET`, `INCR` on `hx:*`, `PING` and `SELECT` for the configured database.

Generator tests parse all three YAML files and check identical endpoint expressions,
required interpolation, TLS paths, private directory permissions, no fabricated
materials and API-only read-only mount. The actual Compose 5.6.1 parser also accepted all three files and refused missing
authority locally; CI repeats the Compose validation. These are configuration
checks, not an actual container/multi-host deployment. Earlier real Valkey mTLS/ACL command tests
prove the client, not this network path. Verify connectivity, ACLs, certificate
trust/name/expiry, outage backend load and observed cache invalidation on all three
real APIs before use. Optional cache connectivity does not gate readiness with
Postgres OAuth state; runtime failures bypass caching as documented below.

Before starting APIs, explicitly probe each node's configured endpoint using the
same API image and read-only TLS mount (this command writes two random scratch
keys; do not run it against an endpoint you are not authorized to test):

```
docker compose --env-file .env --env-file cache.env run --rm --no-deps api /bin/cachecheck
```

`cachecheck` requires `rediss://` and all three TLS files. It uses the application's
verified transport, ACL credentials and selected database. It checks PING, then
SET EX NX / GET under random `hx:rc:preflight:` and `hx:gen:preflight:` keys, then
INCR / GET of the seeded generation scratch key. Both keys are created with
30-second expiry before incrementing; the probe does not touch real tenant keys,
clear the database, retry commands, or require DEL permission. Failed checks stop
at the first error and print a fixed phase, never the endpoint or server error.
A successful check leaves two expiring scratch keys. A lost reply has unknown
commit state and is not retried. Under normal server expiry semantics these
scratch writes expire even after a later probe failure. There is no cleanup
promise against a server with broken expiry semantics.

This opt-in operator command is not part of readiness or startup. Passing proves
only command/key permissions and transport to this endpoint at that moment, not
capacity, cross-node identity, invalidation delivery, persistence, replication,
rotation or failover. A PING-only ACL fails the write probe; a SET/GET-only ACL
fails the increment probe. Real loopback Valkey mTLS tests cover these denials
and TTL preservation, separate from the generated-container/network acceptance.

This does not deploy a Valkey primary/replica, proxy, Sentinel or Cluster, discover
a promoted primary, replicate data or establish persistence/failover/RPO/RTO.
Those remain operator-owned prerequisites and open product work. Do not point
replicas at different independent stores or treat the VIP/database proxy as a cache
endpoint. Failed invalidations can still leave shared data stale until its TTL.

### Secured explicit Redis/Valkey endpoint

`REDIS_URL` accepts `redis://` (plaintext) or `rediss://` (verified TLS), optional
URL-encoded `user:password`, and `/database` (default 0). Existing bare `host:port`
remains plaintext. Use the same endpoint and database on every replica when enabling
`CACHE_BACKEND=redis`. This name selects the RESP cache adapter for Redis or Valkey;
it is not a product conversion or service deployment. Query strings, fragments,
unknown schemes, malformed ports/databases and incomplete credentials are rejected
at startup without printing the URL or credentials.

For a private CA set `REDIS_TLS_CA_FILE`. For mutual TLS supply both
`REDIS_TLS_CERT_FILE` and `REDIS_TLS_KEY_FILE`. Certificates must match the endpoint
hostname (or IP SAN for an IP address), and TLS 1.2 or newer is required. The system
CA pool is used with an optional additional CA bundle. There is no insecure skip
verification option. TLS files with a plaintext URL are rejected. A client certificate
is transport authentication, not a substitute for ACL credentials. Configure ACLs
for the required command/key scope on the server; the client does not create users.
Authentication and database selection run on each short-lived connection before the
command, under the same bounded deadline. Server error text is not returned verbatim.
RESP bulk values are bounded to 8 MiB before allocation; cache writes remain capped
at 1 MiB. No command is retried on another endpoint.

Local proof (2026-10-30): vendor Valkey 9.0.7 Jammy x86_64 binary, SHA-256 checked
against its official sidecar, runs on loopback with plaintext disabled, mutual TLS
required, default user disabled and a restricted app ACL. The test covers PING,
AUTH user/password, SELECT database isolation, GET/SET EX/NX, GETDEL single-use,
INCR, expiration, two independent cache instances sharing hits and invalidation,
ACL key/command denial, wrong password, untrusted CA, hostname mismatch, missing
client certificate, cancelled context and process outage. Five race-enabled test
repetitions passed. CI runs the same test against the pinned hash, separate from
fake RESP tests. Run from `server/`:

```
TEST_VALKEY_BINARY=/path/to/valkey-server go test -race ./internal/redisx -run TestValkeyTransport -count=1 -v
```

This proves the direct transport and these commands, not Sentinel/Cluster,
replication, persistence, failover, rollback or production TLS/proxy-chain behavior.
The generated HA APIs use Postgres OAuth state with response caching off by default.
If an optional shared-cache read fails, the request now bypasses caching and reads
the backend directly. No local fallback cache is read or filled on that request.
A failed invalidation is still not a durable event: a replica that can reach an old
shared entry, or recovery before its TTL expires, can still return stale data. No cache
consistency, no-loss, RPO or RTO guarantee is added.

Sources: [Valkey 9.0.7 binary](https://download.valkey.io/releases/valkey-9.0.7-jammy-x86_64.tar.gz),
[SHA-256](https://download.valkey.io/releases/valkey-9.0.7-jammy-x86_64.tar.gz.sha256),
[TLS](https://valkey.io/topics/tls/), [ACLs](https://valkey.io/topics/acl/).

### Response-cache outage and privacy guards

On configured shared-cache read errors, each request reads the authorized backend
without a local-cache lookup or fill. This removes a second stale-cache window
during an outage, at the cost of more database/backend load. A failed shared
invalidation still allows stale responses through reachable replicas or after
recovery until the original shared entry expires. This is not durable invalidation,
strong consistency or cache HA. A valid shared miss still uses shared caching.

Local and shared caches refuse responses carrying `Cache-Control: private`,
`no-store` or `no-cache`, `Set-Cookie`, any `Vary`, `WWW-Authenticate`, `Location`,
or `Content-Disposition`. Those responses pass through with their original headers
and body. Bodies above 1 MiB are not cached in either path. Scoped users still
bypass the tenant cache. These checks prevent stripping privacy/representation
headers or sharing a variant under an incomplete cache key; they do not replace
handler authorization or redesign user-specific cache keys.

A waiting request can cancel while another request fills the same key; it does
not wait indefinitely for that request or run a second backend handler. The fill
itself still follows its own request context and backend behavior. Local fixtures
test both paths, header preservation, oversized bodies, repeated outage reads and
canceled waiters under the race detector. The real Valkey TLS regression now also
kills the process and confirms two successive cache reads observe successive backend
values rather than a fallback cached result. No network-partition, recovery or
invalidation-replay guarantee follows from killing one local process.

### Shared OAuth state without an independent cache dependency

The HA generator sets `OAUTH_STATE_BACKEND=postgres` on every API and no longer
sets `REDIS_URL` or `CACHE_BACKEND=redis`. Migration 0089 stores only SHA-256
hashes of random state tokens, the nonce/payload, and a database-clock expiry.
`DELETE ... RETURNING` consumes a state once across connections/processes;
expired states are refused and deleted. Each Put removes at most 1000 expired
rows and refuses duplicate tokens instead of replacing an existing nonce.
Operations have a five-second deadline, with no local fallback on errors.
Lifetimes must be positive and no greater than 30 minutes. The application uses
10-minute OAuth state. Expired unconsumed rows are collected by later Put calls,
not a background sweeper; a quiet deployment can retain expired rows until its
next login/connect attempt. Database administrators can see nonce/payload values;
they are not encrypted. Callback errors remain generic.

A real Postgres 14.24 test uses two independent pools to prove cross-replica Put/
Take, 32 racing callbacks with exactly one winner, expiry, duplicate preservation,
raw-state hashing, bounded expired-row collection and failed-pool behavior. The
Google Workspace simulator flow now starts connect on one state store and
consumes the callback through another pool, then refuses replay. No real provider,
Patroni promotion or multi-host network has been tested by these checks.

Existing deployments keep their default: absent `OAUTH_STATE_BACKEND`, Redis is
selected when `REDIS_URL` is configured, otherwise process memory. Explicit values
are `memory`, `redis`, `postgres`; invalid values or Redis without a URL stop startup.
Switch all replicas together. Switching backends discards in-flight OAuth redirects;
users must start sign-in/connect again. No state migration or cache failover is claimed.

References: [HAProxy health checks](https://www.haproxy.com/documentation/haproxy-configuration-tutorials/reliability/health-checks/),
[HAProxy 3.2.26 source](https://www.haproxy.org/download/3.2/src/haproxy-3.2.26.tar.gz),
[source SHA-256](https://www.haproxy.org/download/3.2/src/haproxy-3.2.26.tar.gz.sha256),
[HAProxy support table](https://www.haproxy.org/),
[Valkey compatibility](https://valkey.io/topics/migration/).

## Commands

Run the scripts with `bash` (files published through the GitHub web editor lose the executable bit; `chmod +x scripts/*.sh` also works).

```
bash scripts/install-ha.sh plan   --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10
bash scripts/install-ha.sh render --nodes n1=...,n2=...,n3=... --vip 10.0.0.10 --iface eth0 [--sync-strict]
# copy ha-out/<node> to each server (inside a HexThings checkout), then on each:
docker compose up -d --build
bash scripts/install-ha.sh check  --nodes ... --vip ...
bash scripts/install-ha.sh drill  --vip 10.0.0.10
```
Render refuses a non-empty output folder, generates fresh secrets (shared by the three nodes, `.env` mode 600) and never writes a secret into the compose, Patroni or HAProxy files. Air-gapped sites: build the Patroni image on a connected machine and carry it with scripts/airgap-bundle.sh (the bundle does not yet include the HA images: not built).

## Real tradeoffs

**Data loss vs availability (an operator decision, not a measured guarantee).**
- New output explicitly requests `synchronous_mode: true`, one synchronous standby (`synchronous_node_count: 1`) and PostgreSQL `synchronous_commit: "on"`. These make the intended settings visible rather than relying on defaults. Patroni owns `synchronous_standby_names`; the generator does not hard-code members. These are bootstrap settings, not an update to an already initialized cluster's dynamic configuration.
- Default, not strict: Patroni can disable synchronous replication when no suitable standby is available so the primary can continue accepting local writes. Those writes are not guaranteed to exist on another node. In that state Patroni may refuse automatic promotion after primary loss, leaving writes unavailable until that primary returns or an operator intervenes. A manual failover can lose transactions. Do not treat this as automatic asynchronous failover with bounded loss.
- `--sync-strict` asks Patroni not to disable synchronous replication when no synchronous candidate exists. Commits normally wait for a suitable standby, reducing availability. It does not make every request read-only, guarantee an outage duration or promise zero lost transactions. Sessions can explicitly reduce `synchronous_commit`; cancellation while waiting for replication can expose changes that are not yet replicated and may be lost on promotion. These cases and simultaneous failures need explicit application and operator acceptance.
- Synchronous commit can increase write latency and reduce throughput. Neither that cost nor a durability bound has been measured on this stack. No zero-loss, RPO or RTO commitment follows from these settings.

**Split brain and fencing.** The intended design uses an etcd majority lease and Patroni demotion/promotion rules. A majority lease is not a hardware fence: a stuck Patroni process, failed PostgreSQL shutdown or delayed network state can break the intended write-leader invariant. A node should demote on loss of the DCS lease, but that behavior has not been tested here. Three nodes provide a majority-voting design; this generator supports exactly three and offers no two-node deployment. Loss of two voters is intended to prevent automatic promotion, not a measured proof that every possible writer is stopped. No watchdog is configured or validated. A watchdog/fencing design and real partition/pause/stop-failure drills remain prerequisites for deployment. VRRP can expose the VIP on more than one node during a partition. HAProxy primary checks lag reality and cannot prove a stale primary is fenced, so they do not establish database partition safety.

Source: [Patroni replication modes](https://patroni.readthedocs.io/en/latest/replication_modes.html). This guide explains the intended mode using current vendor semantics; the pinned Patroni image and real cluster still need runtime validation.

**MQTT is the weak spot.** Brokers are not clustered. After a broker failover, persistent sessions and queued QoS 1/2 messages for offline subscribers on the old broker are lost. Telemetry from HexThings edge agents is protected by their local store-and-forward buffer (the agent reconnects and resends), but third-party publishers without a buffer can lose messages in that window. A real broker cluster (EMQX or similar) is the fix; it is not built.

**Not covered.** Elasticsearch/search HA, the optional AI runtime, PostgreSQL/REST/API TLS, etcd RBAC, off-site backups and a tested rolling-upgrade procedure. Opt-in etcd transport mTLS is rendered and locally tested separately below; default etcd remains plaintext. WAL archiving and validated snapshot recovery are not configured. Stateful volumes live on each node's local disk.

## Proxmox notes

Run each node as a VM on a different Proxmox host. Do not also enable Proxmox HA on these VMs: Patroni already handles database failover and double management causes fights. Keep the three VMs on different physical hosts (anti-affinity) and put the VM disks on local fast storage, not a single shared disk.

## Failover drill (must be run on real hardware)

`bash scripts/install-ha.sh drill --vip ...` prints the steps: write a marker through the VIP, stop the leader (then the harder test: power the VM off), observe whether a new leader appears (30 to 45 seconds is an unmeasured target, not a guarantee), read the marker back, restart the old node and confirm it rejoins as a replica, then record the measured time and any lost write here. Until a result is recorded below, this feature is **untested end to end**.

| Date | Hardware | Failover time | Lost writes | Result |
|---|---|---|---|---|
| (not yet run) | | | | |

### Point-catalog completeness for report pickers

`GET /v1/points` now buffers and checks every Scan and iteration error before
returning a result. It reads at most 5001 rows: up to 5000 returns the existing
array shape, while overflow returns HTTP 422 with no partial array. Read errors
return generic HTTP 500. Both failure paths are `no-store` and do not enter the
response cache. A successful empty result remains `[]`. Ordering is stable by
device name, device id and point id; tenant filtering remains unchanged.
Customer-scoped users remain denied this endpoint by the existing allowlist.
This is an explicit completeness limit, not pagination or support for larger
catalogs. Large installations need a paginated/searchable catalog implementation.

Reports now distinguishes catalog loading, failed reads and successful empty
results. Retry clears old choices, validates returned rows and keeps the draft;
a failed read no longer says to onboard a device. This does not validate the
availability of every point already stored in an existing report. Unit fixtures
cover Scan/iteration failures and exact-limit/overflow cases; real Postgres tests
cover 5001-row rejection, uncached recovery, tenant isolation and database outage.

Map position-source setup now treats point-catalog failures as unavailable, not an
empty list. Source selection clears on device change/reload; late responses from
the prior device are ignored. Failed or incomplete reads show retry and block Save
source. A successful read preselects configured latitude/longitude points only if
they are present in that device's returned points. This guards selection, not the
meaning/units of those points. Clear remains a separate explicit write; read/retry
never writes. It still uses the bounded catalog endpoint, so catalogs above 5000
rows remain unsupported here. Map data/trail failure behavior is separate.


### Cache-off HA default (2026-11-01)

`CACHE_BACKEND=off` leaves the API response cache unset, bypassing both middleware and invalidation. Empty or `local` retains the single-process cache for existing installations; `redis` requires an explicit endpoint; unsupported values fail startup. This removes default node-local stale response entries, not every source of staleness: database visibility, client caches, ingestion timing and derived data still have their own semantics. Turning caching off raises database load. Capacity and multi-host acceptance remain unmeasured. The external mTLS cache option still selects `redis` explicitly and keeps its documented TTL/invalidation-failure limits.

Regression tests create two API handler instances over a real PostgreSQL database, confirm neither caches a successful point list, write a new point through that database, and read it on both handlers. After closing the database pool, both fail rather than serve a cached success. Generator tests check both modes contain no unused Redis service/volume/password, PostgreSQL OAuth state remains selected, and the default `off` value stays a string in YAML. This is not a real proxy/multi-host failover test. The printed plan labels one-node survival as an unverified design target.


### Cross-replica OIDC application check (2026-11-02)

`TestIntegrationOIDCCrossReplicaCallback` runs the real login/callback handlers with two independent PostgreSQL connection pools and a local synthetic OIDC issuer (discovery, token exchange, RSA-signed ID token and JWKS). A login started on API A can finish on B, mint a platform token verifiable with A's shared signing key, and create a tracked session visible through A's database pool. Sixteen concurrent callbacks across both handlers produce exactly one successful callback and one issuer exchange for that test state. Replays and database-expired state do not reach the issuer. A bad nonce and unknown platform email are rejected without creating a session or user. A closed state-store pool rejects before exchange; the still-healthy pool can consume that unconsumed state.

This is an application integration test, not a live identity-provider acceptance test or a proxy/multi-host/PostgreSQL failover drill. It does not measure RPO/RTO, network partitions, TLS termination, browser cookie behavior, provider outages or an in-flight database promotion. Shared state alone is not a replicated Valkey service or complete HA acceptance.


### Read-only HTTP checkpoint

Run `python3 scripts/check-ha-http.py --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10` only against infrastructure you are authorized to inspect. It sends one GET each to node API8001/web8081 `/readyz`, node Patroni8008 `/primary`, and VIP API8000/web8080 `/readyz`. It requires HTTP200 on all readiness paths and exactly one primary200 with two non-primary503 responses. Unreachable, unauthorized, redirected, malformed or timed-out responses fail the checkpoint. Default per-target overall deadline is two seconds (maximum ten); probes run concurrently. It does not follow redirects, retry, use environment proxies, send credentials, mutate state or store response bodies/error details. JSON reports logical target names, statuses and elapsed time, not upstream bodies or credentials.

This is a point-in-time set of separate HTTP samples, not an atomic cluster snapshot. A leadership transition can fail the check; inspect before deliberately rerunning it. Passing does not test writes, replication, TCP database routing, MQTT, provider availability, TLS, partitions, fencing, capacity or failover/RPO/RTO. Generated HTTP listeners are not a secure production transport. The drill now uses local `patronictl list` rather than the nonexistent VIP8008 route. Synthetic loopback tests cover redirects, zero/two primaries, authorization errors, no-body responses, refused connections, malformed replies, stalled/trickled headers and invalid input. No real cluster result is recorded.


### Opt-in etcd mutual TLS

`bash scripts/install-ha.sh render --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10 --etcd-mtls --out ha-out` renders HTTPS-only etcd client/peer listeners bound to each node's explicit address. Client and peer certificate verification are required. Initial member URLs are HTTPS. Patroni's `etcd3` configuration uses HTTPS, CA validation and its own client certificate. This option can be combined with `--shared-cache-mtls`; the two PKI mounts remain separate. Default rendering is unchanged and still has plaintext etcd. Neither mode establishes a secure production deployment.

Provision operator-owned `etcd-tls/ca.crt`, `member.crt`, `member.key` and separate `patroni-etcd-tls/ca.crt`, `client.crt`, `client.key` on every node before startup. No certificates, CAs or private keys are generated. Directories start mode700 with instructions only. Use a distinct member identity on each node, whose certificate covers its advertised IP SAN and permits serverAuth/clientAuth. The Patroni identity needs clientAuth and trust under the etcd CA, not the member private key. Set key permissions/ownership for the actual container UID; Patroni runs non-root. Keep private keys600 and directories700, not world-readable. Mounts are read-only and refuse automatic creation of a missing host directory. Missing/malformed/untrusted/expired files must be fixed, never bypassed with plaintext or disabled verification. File loading and transport behavior in the generated container stack have not been exercised. A separate local-binary test below exercises the rendered etcd flags only.

The member material is mounted only into etcd; the separate client material only into Patroni. Certificate possession authenticates a TLS peer, not an etcd RBAC role. Patroni's etcd3 gRPC gateway cannot be assumed to map certificate CN into an etcd RBAC username. This option does not configure RBAC, rotation/reload/revocation, PostgreSQL/REST/API TLS, firewall rules or a rolling migration of an existing etcd cluster. Do not copy these files over an existing cluster and restart blindly: member URL/certificate changes need an operator-managed migration and recovery plan. Existing generated directories are not changed.

Tests parse all three outputs with and without the optional shared cache, checking HTTPS-only URLs, both certificate-auth flags, exact Patroni verification paths, private empty-material directories and service-specific read-only mounts. Compose syntax checks are configuration evidence only, not a real etcd handshake, election, Patroni discovery or multi-host failover result. Official sources: [etcd transport security](https://etcd.io/docs/v3.5/op-guide/security/) and [Patroni YAML configuration](https://patroni.readthedocs.io/en/latest/yaml_configuration.html).

### Local etcd TLS transport regression

`ETCD_BINARY=/trusted/path/to/etcd python3 scripts/test-ha-etcd-tls.py` renders the mTLS option and preserves its transport/authentication flags while relocating all three members to loopback addresses, disposable data directories and temporary synthetic PKI. It generates distinct test member identities and a separate client identity, starts three real etcd processes, requires health on all members, writes through the v3 HTTP gateway and reads the value back through each member. Member-list results must advertise HTTPS client and peer URLs. No-certificate and untrusted-client gateway reads and peer connections are rejected; untrusted-server CA and wrong server IP SAN are rejected. With the client-listener member certificate absent, neither authenticated nor plaintext health is accepted during the bounded check. This does not assert immediate process exit: the first test attempt found etcd could remain running while unable to serve that listener.

The local test now passes with generator-pinned etcd3.5.35 from the official release bucket. Its SHA-256 `ca9f94cdc80be66923ef571d82b128819d0f5f3aa9f01522bafd991618ee29d1` matches the vendor [release checksums](https://storage.googleapis.com/etcd/v3.5.35/SHA256SUMS). CI runs that same fixture with checksum verification. The generator uses the vendor's primary image `gcr.io/etcd-development/etcd:v3.5.35`; its published manifest lists Linux amd64/arm64. The script downloads nothing itself, uses no external account and removes temporary keys/data/processes on completion.

This is not a test of the etcd image, Compose mounts/UID permissions, Patroni's actual TLS client, container networking, real multihost peer IP SANs, RBAC, identity rotation, partitions, fencing or failover/RPO/RTO. The loopback members share an IP SAN because they run on one host; production members must have their own advertised IP SANs. It adds no production trust material and changes no default listener or existing deployment.

The first CI run of this fixture failed before stack startup because its synthetic CA lacked a keyUsage extension required by the runner's strict certificate verification. Enabling `VERIFY_X509_STRICT` locally reproduced that exact error. The fixture now explicitly sets critical CA basicConstraints/keyCertSign/cRLSign, critical non-CA leaf constraints and digitalSignature/keyEncipherment, and enables strict verification in every test client context. The before/after local result isolates this fixture certificate defect; it does not change production certificates, trust policy or etcd flags. Verification was not disabled and the deadline was not increased. Failure-only bounded member logs remain available.

### etcd patch baseline, checked November 3, 2026

The generator and TLS CI fixture move from3.5.14 to3.5.35, staying on the3.5 branch. The vendor's [October22 patch notice](https://etcd.io/blog/2026/oct-22-patch-release/) identifies3.5.35 as a supported security/operational update and recommends updating. Its [versioning policy](https://etcd.io/docs/v3.6/op-guide/versioning/) maintains the current and two preceding release lines. This is not a claim that no newer issue exists: the same notice explicitly says further reports are not yet patched. The [release page](https://github.com/etcd-io/etcd/releases/tag/v3.5.35) names gcr.io as primary registry. The downloaded Linux amd64 binary reports3.5.35, Go1.26.10, and passes the strict three-member loopback TLS/gateway regression with the rendered flags. Manifest availability does not prove either image's runtime behavior or ARM64 execution.

This changes new render output only; it does not upgrade existing generated directories or live clusters. No rolling old-to-new data-dir upgrade, restore, rollback, Patroni compatibility under real load, image execution or multihost failure drill has been performed. Operators must validate a snapshot/recovery plan and stage their dependent services before deciding on an existing-cluster upgrade. The other HA pins and transport gaps remain separate work. Do not interpret one supported etcd patch as a supported, secure enterprise deployment of the whole stack.

### Patroni4.1.5 DCS compatibility checkpoint

New Patroni image builds pin4.1.5 instead of3.3.2. The vendor [release](https://github.com/patroni/patroni/releases/tag/v4.1.5) is dated August12,2026; [release notes](https://patroni.readthedocs.io/en/latest/releases.html) include compatibility with PostgreSQL16.15. This is a current release checkpoint, not a vendor long-term-support promise or a claim that every dependency in the image is audited. The PostgreSQL16 base image remains a floating16-bookworm tag and was not built here.

Set `PATRONI_PYTHON=/path/to/python-with-patroni4.1.5` alongside `ETCD_BINARY` when running `scripts/test-ha-etcd-tls.py`. This optional path invokes `scripts/test-patroni-dcs.py` against the same three disposable etcd3.5.35 TLS members. The helper loads the rendered `etcd3` protocol/CA/cert/key settings, relocates hosts and cert paths to loopback fixtures, and creates two independent real Patroni DCS clients in patronictl mode. One writes an initialization marker and configuration; the other reads it and writes a configuration update; the first reads that update. Fixture namespace deletion is checked. No PostgreSQL, Patroni daemon or production cluster is started. CI installs the matching Patroni version in a disposable virtual environment and requires this checkpoint. The helper checks the version and restricts the fixture to loopback hosts and its isolated namespace.

This passed locally with Python3.10.12 and Patroni4.1.5. It verifies real Patroni DCS HTTPS discovery/config read-write with rendered TLS keys; generic missing/untrusted certificate, CA and SAN rejection remains covered by the separate TLS clients. It does not prove Patroni's daemon loop, leader leases/watchers, PostgreSQL bootstrap/replication/rewind, client-identity rejection through Patroni itself, actual PG16/container UID/mount behavior, rolling3-to4 upgrade or failover. Transitive Python dependencies still resolve during image/test installation; a reproducible offline dependency lock and image build/runtime tests remain open. Existing image builds, generated directories and live clusters are not changed automatically. Stage and validate the complete image, backups/recovery and dependent services before any deployment or upgrade.

### Patroni DCS watch and lease checkpoint

The same optional real-Patroni helper now adds two DCS clients with patronictl disabled, distinct fixture member names and a second isolated namespace. These clients start Patroni's actual etcd watch caches and grant real etcd leases. Assertions check cross-client member visibility, exclusive leader-key acquisition, a keepalive call, rejection of non-owner deletion, voluntary release, acquisition by the other client and rejection of deletion by the former owner. With no further keepalives, the second client's real TTL30s lease must expire: its member and leader keys disappear, then the first client acquires a new leader key. The test polls with a45s expiry deadline rather than assuming an exact expiry instant. The whole Patroni subprocess has a90s outer deadline to cover this intentional TTL wait, instead of the former25s config-only budget. Namespace cleanup is asserted. Failed acquisition can restart the watch stream, producing benign `watchprefix ... Response ended prematurely` diagnostic lines; convergence/ownership/expiry assertions must still pass.

This checks the DCS component in daemon mode, not the Patroni daemon's HA loop. It adds actual watch-cache and leader-key/lease evidence to the earlier patronictl config test. It does not start PostgreSQL, promote a database, test WAL/synchronous replication, process fencing, a network partition, cache recovery across an etcd outage, sustained keepalives across many TTLs or a real multihost failover. Lease expiry is not proof that an old PostgreSQL primary stopped serving writes. The earlier image/upgrade/dependency caveats still apply. CI requires both DCS phases; the plain transport-only invocation without PATRONI_PYTHON runs neither.

### Single etcd process outage and restart checkpoint

The disposable TLS test now stops the first advertised etcd process after the DCS lease tests. The surviving two-member majority must commit a new gateway value and read it from both live members within15s. With PATRONI_PYTHON set, a fresh pair of actual Patroni4.1.5 config clients then performs its discovery/init/config read-write/cleanup phase with the unchanged original three-endpoint list, including the stopped first endpoint. This config-only invocation has a25s deadline and does not repeat the watch/lease phase. Connection-refused/discovery retry diagnostics for the stopped endpoint are expected; successful assertions on the surviving majority are required. Finally the stopped process restarts with the same data directory and generated flags and must serve healthy TLS and the value committed during its absence within20s.

This is a clean process stop/restart on loopback, not a network partition or crash/power-loss/fsync test. No claim is made about a specific raft leader having been stopped, exact recovery latency, an existing Patroni watcher surviving an outage, two-member loss/quorum-loss behavior, permanent membership replacement, PostgreSQL or application availability. It narrows endpoint-discovery/majority/rejoin coverage only. Run without PATRONI_PYTHON for the gateway-only portion; both real Patroni phases require that environment variable and are required by CI.

### Quorum-loss acknowledgement boundary

The disposable TLS fixture additionally sends SIGKILL to two of its three etcd processes. The remaining authenticated endpoint must return the previously committed value through an explicitly serializable read, while a new write must not be acknowledged: only a request timeout or HTTP503/504 is accepted. Connection refusal, unexpected HTTP errors or a successful write response fail the assertion. A second serializable read checks that the endpoint remains reachable after that write attempt. One member is then restarted with its original data to restore a majority, which must commit a fresh new value and read it back; the final member restarts and must read that value too. Recovery checks each have20s deadlines, not service-level latency promises.

Serializable reads can be stale and do not demonstrate quorum or safe leader ownership. A timed-out write has an unknown outcome: it might commit later when quorum returns. The fixture deliberately does not claim that the timed-out key is absent or rolled back. Applications must reconcile uncertain writes with idempotency/operation identifiers instead of blindly retrying a non-idempotent action. This is gateway evidence from loopback process termination, not a network partition, a Patroni DCS failure-path assertion, PostgreSQL demotion/fencing or application-availability proof. Earlier DCS and deployment caveats remain.

### Same Patroni config client through quorum loss

With PATRONI_PYTHON set, `scripts/test-patroni-quorum.py` now keeps one actual Patroni4.1.5 patronictl-mode DCS object alive across the two-process outage. Before the stop, it writes and reads an isolated config marker. After quorum loss, `set_config_value` must return exactly False through Patroni's expected error handling; an arbitrary exception or killing a hung helper is a test failure. After gateway writes/readback prove that quorum is restored, the same object must write/read a new marker and delete its fixture namespace. File handshakes have25s parent deadlines; the helper has bounded waits and is terminated/killed on outer-fixture failure. Expected retry/error tracebacks from the deliberate outage are diagnostics, not ignored test failures.

This adds failure/recovery evidence for a patronictl config client, not an existing watch-cache client, leader lease keepalive, Patroni daemon HA loop, PostgreSQL demotion or fencing. A False result still does not mean the uncertain write cannot commit later; the recovery phase writes a distinct new marker and makes no rollback assertion. No production outage or infrastructure change is performed.

### Existing watch cache and leader-lease failure checkpoint

The same quorum helper now also starts a non-patronictl DCS object before the outage, asserts that its real etcd watch cache is active, publishes its member entry, acquires a leader key and renews its lease. During quorum loss repeated `update_leader` calls must eventually return False or raise Patroni's specific Etcd3Error within a 15s retry window; unrelated exceptions or continued success beyond that window fail. Immediate success is possible after quorum loss and was observed locally; the helper counts these transient successes rather than claiming instant failure. The final call may consume the configured 3s request retry budget. After gateway-verified quorum restoration and a new config marker written by the separate patronictl client, this SAME watch-backed object must see that fresh marker, then successfully call `update_leader` again. Recovery is bounded by a20s marker-convergence deadline; parent handshakes retain their25s bounds.

The two-member outage now deliberately uses SIGKILL, not graceful termination: an active gRPC watch delayed graceful shutdown in the local fixture. This is an explicit process-kill test on loopback with original data directories reused. It does not simulate power loss, storage corruption or a network partition. Earlier references to clean two-member stops describe the older test, not this current phase. The separate single-member stop/restart phase still uses graceful termination.

This adds actual watch-cache recovery and DCS leader-lease failure/recovery evidence. It does NOT run the Patroni HA loop or PostgreSQL and proves no demotion/fencing, old-primary shutdown, database promotion, replication or client availability. The DCS update result concerns a key/lease, not a server role. No claim is made about lease expiry during this short outage or a sustained keepalive soak. Expected retry/timeouts/watch-stream errors remain visible; only bounded eventual failure and fresh-marker recovery satisfy the test. A transient successful keepalive is not proof of a usable write quorum. Uncertain write outcome and dependency/image caveats still apply.


### Real Patroni transport TLS rejection checkpoint

The optional real-Patroni fixture now calls `test-patroni-tls-rejection.py` before the config/watch/lease phases. It constructs the actual Patroni4.1.5 `PatroniEtcd3Client` transport with the rendered HTTPS endpoints relocated to loopback and the same cert/key and CA mapping used by Patroni's DCS constructor. Three cases must fail discovery: no client identity, a client signed by an untrusted CA and a server CA the client does not trust. Good-identity discovery must return all three members both before and after the negative cases.

This helper deliberately constructs the transport directly: `get_dcs` retries startup indefinitely on discovery failure, so killing that constructor on a timeout would not establish TLS rejection. The direct constructor must raise the specific plain `etcd.EtcdException` from failed discovery, with an SSL-handshake diagnostic for each of the three endpoints. Connection refusal, request timeouts, unrelated Python errors or a successful discovery fail the test. Recognized diagnostics include certificate alerts/verification errors and `SSLEOFError`: Python/OpenSSL can report the server closing a missing/untrusted-client handshake as EOF rather than a named certificate alert. The controlled identity changes plus fresh positive controls establish rejection; EOF alone is not a certificate-reason diagnosis. An outer25s subprocess deadline is a failure safeguard, never a passing negative result.

This adds real Patroni transport negative-handshake evidence, not full Patroni daemon startup/error handling, hostname/SAN rejection through Patroni, server-side subject authorization, revocation/expiry, certificate rotation or PostgreSQL/REST/API TLS proof. Generic wrong-SAN rejection remains in the separate HTTPS fixture. All certificates are disposable synthetic test PKI; no live credentials or deployment settings change. Existing DCS/HA-loop/database, dependency-lock and image-runtime limitations remain.


### Actual Patroni hostname verification checkpoint

The transport rejection helper now also supplies `https://localhost:<fixture-port>` endpoints while the member certificates contain only `IP:127.0.0.1`, never `DNS:localhost`. It confirms that localhost resolves only to loopback and that every alias port accepts a TCP connection before invoking the unchanged real Patroni transport. All three endpoint diagnostics must include certificate verification failure and an explicit hostname mismatch. Generic SSL EOF, connection refusal, timeout or another exception does not satisfy this SAN case. Fresh good-identity discovery through the original127.0.0.1 endpoints still succeeds before and after all negatives.

This passed locally with Patroni4.1.5, Python3.10 and the disposable etcd3.5.35 fixture. It adds actual Patroni transport hostname-verification evidence, superseding the earlier SAN exclusion for that narrow transport layer. It does not test full daemon startup, DCS configuration reload, certificate rotation/revocation/expiry, client subject authorization or PostgreSQL/REST/API TLS. No hostname verification is disabled, no trust defaults change and no real certificates or deployments are touched. The25s outer deadline remains a failure safeguard, not a passing rejection result. All HA-loop, database, image and dependency-lock caveats remain.


### Real DCS revision-guard checkpoint

The real Patroni config helper now captures an etcd config revision, conditionally writes a newer value, then tries an update with the old revision. The stale write must return exactly False; another independent client must still read the newer value at its unchanged revision. A subsequent write using the current revision must succeed. This exercises Patroni's real etcd compare-and-swap path over mutual TLS, including the existing one-endpoint-down config-only rerun.

The same helper also creates `/sync` metadata only if absent (version0), rejects a second create, updates it using the observed current revision, and rejects both a stale update and stale conditional delete. The newer state must remain exactly intact. Deletion with its current revision must succeed and another client must see no sync leader. Patroni4.1.5 returns None for a rejected `write_sync_state` and an empty dictionary for a rejected etcd `delete_sync_state` transaction, despite the latter's boolean annotation; the fixture checks the observed empty result and the preserved state rather than claiming exact False. Unexpected exceptions/timeouts fail, and fixture namespace cleanup remains required.

This is coordination-store revision/transaction evidence, not actual synchronous PostgreSQL replication, standby health, quorum-election safety, promotion, demotion/fencing or write availability. The fixture writes synthetic leader/standby names without starting those database members. It does not test concurrent thread races, a full HA loop or stale-revision behavior during quorum loss. Existing image/dependency/TLS/upgrade limitations remain. No production keys/configuration are changed.


### Competing-writer DCS checkpoint

The config helper now releases two independent Patroni DCS clients from a thread barrier against the same observed revision with distinct payloads. Each config race must return exactly one True and one False. Both clients then read the winning payload at a changed revision. For `/sync`, both writers use the same initial revision; exactly one returns a new SyncState and the other None, both clients must read that winning state, and deletion using its current revision must remove it. Three rounds run for each key in both the healthy-cluster phase and the existing one-endpoint-down config-only phase. Threads have a5s barrier deadline and10s result waits, inside the existing subprocess deadline; arbitrary exceptions, hung threads or two/no winners fail.

This adds barrier-released competing-writer evidence to the earlier sequential stale-revision tests. It does not promise simultaneous network arrival, exhaustive interleavings, cross-host behavior, stress/soak coverage or correctness under quorum loss. Exactly one winning metadata write in each tested race is not an exactly-once application-delivery guarantee. No database members are started; synchronous replication, election/promotion, fencing and Patroni HA-loop claims remain excluded. No production keys, defaults or deployments change.
