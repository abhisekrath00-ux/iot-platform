# Multi-node high availability (3 nodes)

Single node: `scripts/install.sh` (already built, unchanged, see docs/install.md). Multi node: `scripts/install-ha.sh` (this page). One entry point offers both: `bash scripts/setup.sh` shows a menu (1 single node, 2 multi node), or `bash scripts/setup.sh single|multi [args]`. The menu itself is tested only for the non-interactive paths; the interactive prompt has not been exercised on a terminal.

**Status, stated plainly.** The generator is tested (`scripts/test-install-ha.sh`: input validation, rendering, YAML structure, secrets handling, file modes). The generated HTTP routing has also been exercised with a real local HAProxy 3.2.26 binary and three synthetic HTTP backends (`scripts/test-ha-http-routing.py`). This is not a rendered-stack test. The rendered stack (etcd, Patroni, HAProxy, keepalived, the Patroni image) has **never been started**: there is no Docker and no second machine in the build environment. Every failover claim below is a design, not a result. The drill at the end must pass on real hardware before you rely on it. The generator's own test caught two real bugs in the rendered files (an unquoted etcd host list and an invalid volume list), which is a sign the rendered files likely still hold more.

## What you get

Three nodes and one virtual IP (VIP). Every node runs the same set:

| Part | Mode | Failover behaviour (design) |
|---|---|---|
| etcd | 3-member cluster | Holds the "who is the Postgres leader" lease. Needs 2 of 3 up. |
| Patroni + Postgres 16 | 1 leader, 2 replicas, synchronous replication | Leader lease expires (about 30 s), a replica that has all committed data is promoted, the old leader is rewound with pg_rewind when it returns. |
| HAProxy | on every node | Port 5000 always goes to the node Patroni says is primary. Web 8080 and API 8000 are load-balanced across all nodes (active-active). MQTT 1883 goes to one broker, the others are backups (active-passive). |
| keepalived | VIP floats between nodes | The VIP moves to another node if HAProxy or the node dies. |
| api, ingest, mcp, web | active-active on all 3 | Stateless. Ingest replicas share the MQTT load with shared subscriptions (`INGEST_SHARED_GROUP`). The scheduler runs on one replica at a time (Postgres advisory lock). |
| OAuth state | shared Postgres, `OAUTH_STATE_BACKEND=postgres` on every API | Login and Google-connect callbacks use atomic single-use state shared across API replicas. Database outage fails closed. Existing tracked bearer sessions also live in Postgres. |
| redis | one per node, currently unused by generated APIs | Independent stores cannot share invalidation. Generated APIs now use local response caches instead of falsely presenting those stores as shared. Cache data can remain stale for its configured TTL after a write on another replica. |
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

**Valkey is not generated as an HA service yet.** The current generator and optional single-node
profile still use Redis. The direct client now supports verified TLS and ACL authentication
to an explicitly supplied Redis/Valkey endpoint; that does not create a replicated service. Valkey's vendor documentation states RESP2/RESP3 and
Redis OSS 7.2 compatibility. HexThings uses AUTH, GET, SET with EX/NX, GETDEL and
INCR through a small direct TCP client. Actual Valkey 9.0.7 TLS/ACL command tests now pass locally; this is not a migration
or failover test. The client has no Sentinel discovery or Cluster MOVED/ASK handling. A proxy layer would not by itself replicate independent
stores or make OAuth state available on every API. The generated APIs no longer route OAuth state or response-cache invalidation
through those independent stores. A shared, secured Valkey endpoint and its
failover/persistence policy still need design and verification; no Valkey proxy,
cache HA or RPO/RTO promise is made here.

### Opt-in generated external shared cache with mutual TLS

Default rendering still uses Postgres OAuth state and local response caches.
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
stays unchanged. This adapter works with tested Valkey RESP commands too. The
unused node-local Redis services are still rendered and are **not** a fallback
shared service. Existing generated directories do not change automatically.
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
The generated HA APIs still use Postgres OAuth state and local response caches.
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

**Data loss vs availability (the main choice).**
- Default (synchronous replication, not strict): a committed write is on the leader and one replica. If one node dies, no committed write is lost. If both replicas are down, Patroni falls back to asynchronous so the system keeps running, and during that window a leader failure can lose recent writes.
- `--sync-strict`: writes stop (the database becomes read-only for writers) rather than ever run without a synchronous replica. Zero data loss, lower availability. Pick this if losing a write is worse than a short outage.
- Cost of synchronous replication: each commit waits for a replica, so write latency rises by one network round trip and ingest throughput drops. Not measured here.

**Split brain.** Only the node holding the etcd lease may be primary, and etcd needs a majority, so a node cut off from the other two demotes itself. This is why exactly 3 nodes are required: two nodes cannot tell a dead peer from a broken link, so they cannot fail over safely without a third voter. Two-node setups are not offered. Losing two nodes stops writes by design. Add a hardware or Proxmox watchdog for fencing a hung node: recommended, not configured here. The VIP can briefly sit on two nodes during a network partition; the database stays safe because HAProxy only sends port 5000 traffic to the Patroni primary.

**MQTT is the weak spot.** Brokers are not clustered. After a broker failover, persistent sessions and queued QoS 1/2 messages for offline subscribers on the old broker are lost. Telemetry from HexThings edge agents is protected by their local store-and-forward buffer (the agent reconnects and resends), but third-party publishers without a buffer can lose messages in that window. A real broker cluster (EMQX or similar) is the fix; it is not built.

**Not covered.** Elasticsearch/search HA, the optional AI runtime, TLS between nodes and to etcd, etcd authentication, off-site backups (use docs/backup-restore.md and Patroni's WAL archiving, not configured), and rolling-upgrade procedure. Stateful volumes live on each node's local disk.

## Proxmox notes

Run each node as a VM on a different Proxmox host. Do not also enable Proxmox HA on these VMs: Patroni already handles database failover and double management causes fights. Keep the three VMs on different physical hosts (anti-affinity) and put the VM disks on local fast storage, not a single shared disk.

## Failover drill (must be run on real hardware)

`bash scripts/install-ha.sh drill --vip ...` prints the steps: write a marker through the VIP, stop the leader (then the harder test: power the VM off), expect a new leader in about 30 to 45 seconds, read the marker back, restart the old node and confirm it rejoins as a replica, then record the measured time and any lost write here. Until a result is recorded below, this feature is **untested end to end**.

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
