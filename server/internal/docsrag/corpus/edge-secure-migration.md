# Existing lab identity to secure setup

Status: local implementation and synthetic tests, not an owner rollout or a
complete server provisioning flow. No owner CA, key, token, identity or host
was changed. No remote release, main merge or owner update is authorized.

## Implemented path

1. The stopped Windows manager offers Request secure pairing. The agent
   signs a CSR with the saved private key, stages the public CSR before sending,
   and reuses it after a lost response. Missing/weak/changed keys fail; no
   replacement key, claim, token or gateway is generated. HTTPS is required
   except loopback tests; redirects are refused before token disclosure.
2. The server binds the saved gateway JWT subject to its own active row and
   serial. Pending requests expire after 15 minutes, deduplicate the exact CSR,
   and allow at most three requests per gateway per hour. No certificate or
   gateway fingerprint is changed by requesting.
3. The dashboard's explicit Load pairing requests action is workspace-admin
   only. API keys, assistant calls and customer-scoped users cannot approve.
   The admin compares the local SPKI SHA256 fingerprint and approves the exact
   key/CSR hashes. A transaction locks the gateway and request, checks active
   status/expiry and existing fingerprint, and signs only with the configured
   deployment device CA. Repeating an issued approval does not reissue.
   Pending requests can be revoked; this is not implicit revocation of a
   certificate already issued. Certificates still need a separate operational
   revoke/ACL workflow if the owner later decommissions them.
4. Finish secure pairing retrieves only that gateway's public certificate and
   both distinct CAs. It verifies key equality, serial, chain, lifetime and
   client-auth EKU. Device CA and broker CA are deliberately separate.
5. Before changing any certificate file, it verifies the broker hostname/IP
   and trust chain using the new client certificate, then publishes a fresh
   non-retained nonce only to its own dedicated pairing-proof topic. Independent
   ingest must record that exact nonce/tenant/gateway/certificate and return its
   HTTPS receipt before files are installed. CONNECT, publish exit0 and broker
   reload alone are not acceptance. No subscription, scan, device/source command
   or meter reading is sent or proved.
6. A process-scoped OS lock and recoverable journal cover identity.crt,
   ca.pem (broker trust) and device-ca.pem (issuer trust). Private key,
   identity.json and source config are never overwritten. Normal service start
   refuses a pending transaction. Explicit Recover pairing restores every old
   file after interruption; a corrupt journal fails closed. The manager
   requires the service stopped before pairing/recovery. Power-loss durability
   on native Windows remains unproven. Native service lifecycle/file-retention
   coverage does not prove UAC, reboot, power-loss durability or physical USB.
7. Connect / get config then uses the existing staged checked import path.
   Signed source updates remain separately opt-in. Source apply, confirmed
   reload and a recent measured reading remain distinct completion conditions.

## Verified locally

- Full server suite against a fresh Postgres database, including own-gateway,
  cross-tenant, role/key denial, exact-hash approval, replay, expiry and
  persisted framing/metadata tests.
- Full edge suite plus race tests for setup/command handling. Public CSR retries,
  redirect refusal, missing/changed key behavior, separate CA validation,
  corrupt/incomplete journal failure, rollback/committed recovery and OS locks.
- An opt-in real disposable Mosquitto test uses ephemeral synthetic device and
  broker CAs with a certificate-required TLS listener. MQTT authentication and
  certificate-file install pass while the original key and identity.json stay
  byte-for-byte unchanged. The broker is shut down after the test.
- Web build and 350 tests; desktop/mobile pairing and port-picker pixels.
- Windows executable cross-build, PowerShell5.1/7 contracts and native service
  lifecycle/file-retention tests pass on the disposable standard Windows runner.
  These do not prove owner Windows11 UAC/reboot, GUI clicks or physical USB.

## Deployment gate still open

The checked default docker-compose.yml is lab-only: broker 1883 is published
on host loopback; api/ingest use plaintext internal MQTT; device CA/TLS broker
files and EDGE_MQTT_* secure endpoint settings are not wired by that compose
file. Merely editing .env therefore does not provision this path.

The owner deployment needs an approved network route and a usable HTTPS API,
TLS MQTT server certificate with correct SAN, device issuing CA, distinct
broker trust bundle where applicable, service client certificates, per-gateway
ACL generation and supported broker reload. The API ACL header now includes
source-config publish, which is necessary on an authenticated broker; the
compiled authenticated source-apply harness described below passes. The migration UI
truthfully says issued is not connected and does not silently expose the lab
listener, generate a CA, skip verification or turn on commands/discovery.

A server-menu provisioning path is still required. Candidates are an approved
private LAN-only deployment or approved private VPN route, both with verified
TLS. Actual address/domain, trust provisioning and network/firewall choice are
unresolved, not defaults and not permission to mutate the owner host. Do not
present the helper/API/UI work as nontechnical end-to-end completion.

## Explicit serial inventory, not probing

The local manager can Show local USB ports (OS names only) or explicitly Send
USB ports to dashboard over its saved authenticated HTTPS route. The server
stores only the owning gateway's inventory. The wizard offers a dropdown only
after an explicit load; lists older than five minutes are hidden. No port is
automatically chosen/opened, no slave-address sweep occurs and no collection
starts. Manual override remains inspectable. Actual owner USB/driver/port and
meter settings still need confirmation from evidence.

## Private server preparation menu (local only, not activation)

The single-node terminal menu now offers Prepare private TLS setup, also
`bash scripts/setup.sh secure-prepare`. It asks only for a previously approved
private IPv4 route, confirmed API/broker host, existing certificate directory
and a new private output directory. A non-root shared group ID is explicit,
including when the terminal menu is run with sudo; group0 is refused.
Only RFC1918 private IPv4 ranges are accepted. Cancel changes nothing. It reads the
current resolved Compose and validates existing certificates without generating
any CA, key or account. The resolved input/output contains deployment secrets:
private temporary file cleanup and output permissions are part of this path.
Never attach or share the resolved Compose file.

Required existing files are device-ca.crt/device-ca.key (issuer),
broker-ca.crt, broker.crt/broker.key, https-ca.crt,
https.crt/https.key, api.crt/api.key and ingest.crt/ingest.key.
Broker SAN must include both `mosquitto` for internal services and the confirmed
external hostname; HTTPS SAN must match that hostname. API/ingest certificates
must be client-auth certificates with their exact service CN. Existing signing
key must already be in the resolved API environment. Validation rejects wrong
chains, short lifetime, wrong purposes, SAN mismatch, key mismatch, public or
wildcard bind addresses, unsupported existing exposure and missing signer.
It does not grant permission to create the missing files.

The full rendered JSON replaces the lab broker listener rather than merging a
second anonymous listener. API and ingest use certificate-authenticated MQTT;
device issuer and broker trust are distinct mounts. A TLS web proxy shares the
API network namespace and connects to the API on loopback, with only the
specified private bind exposing 8443 (HTTPS) and 8883 (MQTT). Existing host
loopback web/API ports stay loopback. Shared-group file access is explicit;
supplied certificate file ownership/permissions are not silently changed.
The broker starts directly as the image's mosquitto user; starting as root
would let its later privilege drop discard the shared supplemental group
and make the private ACL/key paths unreadable.

The bundle intentionally lacks a generated gateway ACL until the full current
ACL can be safely produced and reviewed. There is no Activate button/command:
Compose validation, permission preflight, ACL preservation/reload, backup,
HTTPS trust installation, startup/health/rollback and safe selection by future
menu upgrades remain open. Do not call this preparation-only renderer a
complete secure deployment or instruct an owner to run it unattended.

Four local renderer/certificate tests pass, including synthetic distinct CAs,
matching and wrong SANs, separate mounts, private-only bindings and unchanged
input certificate keys. Docker is not available in the current test workspace,
so rendered Compose/container permissions/proxy startup are unverified.

Authenticated source-apply acceptance now also passes in the real compiled
edge + TLS broker with generated gateway ACL + compiled ingest + PostgreSQL
harness: signed source -> applied -> confirmed -> synthetic measured110 with
same-process polling reload. This proves the tested service/topic path; it
is not an owner deployment, Windows11 test or physical meter measurement.

A disposable Ubuntu-only Compose acceptance harness now exercises the rendered
private bundle with synthetic CAs, API/ingest binaries, container shared-group
key/ACL access, authenticated MQTT and anonymous refusal, plus HTTPS web/API
routing. It must pass on the standard native runner before being cited as
container evidence. Even a pass does not implement activation/rollback, install
owner trust, confirm a route or prove a physical meter. No owner credentials
or saved identities are used by this harness.

The Windows native lifecycle test also runs the setup package tests on Windows
to exercise the real OS file lock and saved-file recovery paths. These are
synthetic file/HTTPS tests, not a power-loss, reboot or physical-broker test.

## Full gateway ACL snapshot (local operator, not activation)

`tenantctl broker-acl-export` emits the full current server-wide certificate
ACL from a repeatable-read, read-only database transaction. The operator must
capture output to a private staging file, not post it or send it to a user.
No gateway row or existing broker file changes. Query/scan/transaction failure
emits no partial ACL. Active certificate gateways across every workspace are
included; inactive/revoked gateways and unissued lab identities are excluded.
Direct certificate devices retain only telemetry publish, not control reads.
Reserved service names, duplicate serials and unsafe identity characters are
refused. Active password-auth direct devices block this certificate-only
snapshot instead of silently removing their authentication. A private broker
requires a separate supported migration for them.

This command is not a reload or activation. The synthetic Docker harness
stages a DB snapshot, atomically replaces its disposable ACL, signals HUP and
checks own-gateway MQTT publish and cross-tenant refusal. Only its completed
green run can establish that result. Owner activation still needs a private
backup/journal, exact current image/config selection, supported rollback,
HTTPS trust, current gateway issuer compatibility and safe future menu upgrades.
An empty service-only ACL is still not an acceptable owner migration.

## Read-only deployment preflight (not an Activate command)

`secure-preflight.py --bundle ... --project ... --root ...` rechecks the exact
prepared/current Compose, existing running single-node project, immutable
current image IDs, health, certificate/key/SANs and complete ACL export using
the current API's local operator binary. It never builds, pulls, starts,
restarts, reloads, changes the network or edits DB/broker configuration. A
private receipt hashes the exact inputs and records `activation_allowed:false`.
A failed retry removes an older receipt instead of leaving stale success.
Receipts are private diagnostics and never permission or activation proof.

If current certificate gateways exist, the candidate device issuer must match
the current API's inspectable issuer file bind in DER bytes. Uninspectable or
different issuers fail closed. This proves that unchanged issuer only, not
edge reachability or revocation semantics. A current image lacking the new
ACL exporter is refused; this helper never silently runs a candidate image.
Baseline code/image update and operational rollback remain separate work.

The renderer and synthetic clients now pin Mosquitto to an immutable registry
manifest digest instead of floating `:2`. The prior green shared-group behavior
must be reaccepted under that exact digest before citing compatibility.
This bounds the future file-ownership warning to the tested image; it does
not remove the need to review ownership on a future broker upgrade.
Local preflight tests use fake Docker reads and real synthetic certificates;
no owner deployment or activation has been tested by those tests.

## Deployment journal and selection guard (internal scaffolding only)

`secure-state.py` adds an owner-only deployment journal with an OS lock,
atomic replacement and fsync of each state file and its directory. It binds
matching private preflight evidence, exact old/new resolved Compose files,
current immutable image IDs and the certificate/configuration input manifest.
The preflight receipt remains explicitly non-authorizing. There is no Activate
command or executor, and no journal is created by ordinary menu use.

The journal has prepared, changing, verified, committed and rollback-required
states. Pending, corrupt, missing-committed or changed input state refuses
service operations instead of selecting lab Compose. A partial selector written
before the commit mark also remains blocked. Recovery returns the exact prior
configuration and image IDs as a private plan, not a command that has restored
anything. Rollback settlement removes any partial candidate selector but stays
blocked: selecting and verifying the restored deployment is still executor work.

The terminal service menu and backup/restore/local-admin recovery route a
committed selection through `selected-compose.py`, under the same OS lock for
the lifetime of the Docker call. The legacy installer and Upgrade refuse any
secure state before logging, backup, source changes or service mutation. The
legacy Health check cannot report secure acceptance, and Open suppresses the
old HTTP address. Direct operator Docker commands and unrelated HA/installation
scripts are not governed by this guard. Secure Upgrade is deliberately blocked,
not implemented or recommended for an owner installation.

Local synthetic tests cover interruption at each phase, selection-before-commit,
OS locking, private-file/symlink rejection, certificate/config tamper, receipt
mismatch, shell entry-point refusal and exact project/config arguments. The
existing lab Upgrade tests and real local PostgreSQL backup/restore shim still
pass. None proves service rollback, power-loss recovery, activation, owner TLS
trust, owner Windows/USB operation or recent physical meter readings.

## Fresh broker authorization receipt after pairing

Certificate-only prepared deployments now use the complete validated ACL
snapshot on approval and explicit Finish retries. A non-root local supervisor
watches only that generated ACL and sends HUP on atomic replacement. It has
no Docker socket or access to an owner account. Missing/unreadable ACL stops
the broker rather than silently keeping old grants. HUP delivery itself is
not proof that a gateway has usable authorization.

Finish now requires a separate one-use nonce receipt before changing public
certificate files. Using the saved own-gateway HTTPS identity, it creates a
60-second challenge bound to the current issued migration, serial and exact
certificate fingerprint. It publishes only that nonce, non-retained, to
`t/<tenant>/g/<gateway>/pairing/proof`. Certificate-only ingest records the
observation only while the challenge and current identity still match. Finish
checks the independent HTTPS receipt before committing certificate files.
No MQTT subscription, device command, scan, source start or meter read occurs.
The proof is diagnostic traffic, never telemetry or a measured reading.

The endpoint and ingest handler are disabled outside explicit certificate-only
TLS mode. That configuration flag is not a substitute for deployment acceptance:
activation must still prove the actual listener refuses anonymous clients and
foreign-topic publication. Direct certificate devices have no pairing grant.
A repeated Finish creates a new challenge and invalidates an older receipt.
Wrong topic/tenant/gateway/nonce, replay, expiry, replaced challenge, changed
certificate/serial and revoked identity cannot create a current observation.

Local acceptance used compiled edge and ingest, real disposable Mosquitto,
real PostgreSQL and API handlers. It first withheld the HTTPS receipt despite
broker traffic and required timeout with certificate files unchanged; a new
explicit attempt then observed a fresh receipt and installed the certificate,
retaining the saved key, gateway identity and config. The initial HTTP authentication
fixture bound a synthetic gateway. A later local repeat now uses the production
JWT/API-key, live-user, customer-scope and write-cache middleware with signed
synthetic gateway/admin tokens; missing/bad tokens, wrong role and a different
gateway fail closed. It still runs API handlers in a test HTTP server, not a
full deployed owner API.
The broker image/supervisor behavior still requires the disposable native
exact-image run. No owner activation, USB/hardware operation, source apply or
recent measured reading is established by this pairing test.


## Immutable runtime/rollback configuration builder

`secure-runtime.py` constructs private data only; it calls no Docker command
and has no activation CLI. Its four synthetic tests require exact local image
IDs, remove build/development behavior and mutable pull policies, retain
absolute resolved mounts, and reject missing ordinary services, unknown image
identities, replication and unresolved dependencies. Unverified inactive
profiles are removed from the constructed config, not retained as latent
build/tag actions. An already-active optional profile needs its exact current
image ID. Existing API, ingest, web, database and other application images must
stay unchanged across transport migration; only the expected broker and new
secure-web image may differ, after independent image inspection.

This helper is now wired into read-only preflight and synthetic transaction
acceptance below. It is not an owner activation command or secure Upgrade.
Its mapping inputs have not been established by live inspection on an owner host. A future executor still needs a
fresh current-state check, exact prior/candidate runtime snapshots, a verified
private database backup, explicit downtime/route/trust authority, a complete
ACL snapshot after writers are stopped, acceptance, durable rollback/recovery
and a verified restored selection. Current-image ACL export during activation
must not silently substitute a candidate operator binary. The ordinary lab
Upgrade/bootstrap and the configuration-only transport migration remain
separate operations.

## Transaction engine and disposable Docker adapter (not owner activation)

Preflight now inspects only already-local current/proposed images, verifies
that the broker image proves the pinned registry digest, and emits private
immutable `previous-runtime.json` and `candidate-runtime.json` plans. It binds
those plans and prior read-only file mounts into the receipt. Missing local
images refuse without pulling, building or running. Inactive profiles without
verified image IDs cannot become latent menu builds. This is still read-only
inspection plus private diagnostic files, not permission or a service change.

`secure-transaction.py` is an internal engine with no CLI. Its seven synthetic
adapter tests enforce order: current authority/target check, fresh revalidation,
verified private backup, durable journal, stopped writers, complete ACL export
from the existing current image, staged ACL, candidate start, acceptance and
only then selection. Failures require exact prior-image/config acceptance and
a durable restored selector. Process-interruption cases remain blocked until
explicit recovery. A crash before the changing marker needs prior-runtime
verification, not speculative service mutation. Database restoration is not
part of configuration-only rollback: an older backup could erase newer
telemetry. App/schema changes are a separate update, never smuggled into this
transport transaction.

`secure-disposable-docker.py` is an effect adapter restricted to a synthetic
`hexthings-secure-ci-*` project in a fresh `/tmp/hexthings-compose-ci-*` fixture
on a disposable standard Linux Actions runner. It refuses owner targets and
has no activation CLI. Its real baseline/migration/post-start-failure/rollback
harness passed all three standard native jobs at checkpoint4569786. The raw
Linux log verifies exact rollback with a row created after backup retained,
then a separate positive transaction with committed candidate selection.
The temporary test branch was deleted; main and owner systems stayed unchanged. It uses the exact
prior API image for a one-shot stopped-writer ACL export, verifies a private
pg_dump with pg_restore listing, checks exact images/running health, HTTPS and
MQTT CONNECT plus anonymous refusal, and never restores the database during
rollback. These checks do not prove cross-tenant ACL isolation by themselves;
the separate full ACL/watcher harness supplies that evidence.

No production effect adapter, owner activation command, operational old-API
bootstrap, secure Upgrade or owner runtime recovery is available yet. Owner
route/hostname/trust/downtime/issuer authority and physical gateway/source
acceptance remain separate gates. A passing disposable test must not be used
as owner-host permission or proof of current owner state.


## Read-only current application readiness (not bootstrap)

The server menu now offers "Check secure setup readiness", separate from TLS
preparation. `setup.sh secure-readiness PROJECT` inspects only the existing
Compose project and its already-running API operator. It never pulls/builds
images, launches a candidate operator, creates a journal, restarts services,
exports an ACL, changes the database or activates a deployment. Pending or
damaged secure state refuses before a lab Compose fallback. Resolved secrets
and private ACL/certificate text are not printed.

`tenantctl setup-capabilities` is versioned JSON and works without connecting
to a database. `tenantctl setup-readiness` reads the packaged migration names
and the current migration ledger in a read-only repeatable-read transaction.
It never creates a missing ledger or applies migrations. A missing ledger,
pending file or unknown recorded file requires a separate application/schema
bootstrap. Matching names do not prove unchanged migration contents, current
schema shape, gateway authorization, TLS trust or owner permission.

Current known service roles include API, ingest and MCP as database writers;
Postgres, Elasticsearch and Redis as stores; broker, web and optional local
model as separate roles. An unclassified active service, unexpected database
access or changed writer command refuses classification. Inactive profiles
remain inactive. This inventory is a prerequisite, not an exhaustive proof
that no process outside Compose can write. A production activation adapter
must establish and pause every writer before its final ACL snapshot.

An old current API without this operator protocol fails as unverified. No
candidate operator or SQL ACL fallback runs. The application bootstrap and
production activation/recovery/secure Upgrade workflows are still unfinished.
The new readiness check passed standard native acceptance at checkpoint
1d5ddfb: https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43198360452 .
The raw Linux log includes the current-operator/ledger check before both
transaction paths. Terminal safety descriptions were shortened to fit at80
and100 columns; the separate local PTY suite checks complete80-column hints.
That PTY suite is not part of this native workflow. No owner installation
has been inspected or changed.


## Reviewed additive application bootstrap engine (no operator interface)

`server/internal/setupbootstrap` is separate from transport migration. It has
no CLI or production caller. A future bootstrap adapter still needs current
target/authority, exact old/new application images, verified backup, every
stopped writer, old/new acceptance and durable process-interruption recovery.
Do not interpret its Go result as permission or deployment readiness.

The internal engine checks exact content hashes of all93 packaged migration
files against a reviewed embedded catalog. It accepts only the complete
0001-0089 ledger as a pre-bootstrap history, and only the four exact additions
0090-0093. Unknown/partial history, a missing ledger, unrecorded added objects
or connection-default column, changed/missing/extra/symlink migration files
refuse. All four additions and their ledger rows use one transaction and the
shared migration advisory lock, with bounded statement/lock waits. No existing
DB archive is replayed. A fully recorded93 ledger is returned as recorded, not
as proved schema shape or runtime compatibility. Catalog hashes cover the
packaged SQL, not the bytes previously run on the host.

Isolated PostgreSQL tests pass for injected failure at the last ledger append
rolling all four additions back together, retained gateway serial/certificate
fingerprint and source config, no-op repeat, and refusal cases. Native testing for the original package passed at eece8e0:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43200991572 .
The optional old-API test explicitly skipped natively and retains its separate
local scope. The shape/lock and fake-effect application-coordinator tests below also passed
all three native jobs at35f59bb:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43202970945 .
The raw Linux log verifies the structural checks against PostgreSQL16.

`test-bootstrap-old-api.sh` builds the exact locally present bfe5d89 Git source
in a temporary directory without network fetch or changing a checkout. With
a dedicated test DB, it applies only the reviewed additive engine to an isolated
0001-0089 schema, boots that unchanged old API and checks authenticated gateway,
profile and device reads. Retained device configuration is checked directly
in the DB. This local test passes. It is limited backwards-read evidence, not
complete compatibility, a full bootstrap/app-image-switch, backup/writer-stop,
physical source reading or owner runtime recovery proof. It is not an owner
command. The ordinary old API's startup migration loop is not silently used
as a replacement for the atomic bootstrap engine.


## Application transaction and schema recovery checks (internal, unfinished)

The new `secure-application.py` is a separate internal transaction coordinator,
not an owner command or production Docker adapter. Its immutable plan permits
only API/ingest/MCP image changes to the same exact new server bundle. All
other config, environment, ports, trust, commands, mounts, services and inactive
profiles remain unchanged. It cannot combine application and TLS migration.

The coordinator requires authority and fresh target checks through its effect
adapter, a verified private backup before downtime, then stopping and checking
all writers. A schema-attempt marker is fsynced before calling the reviewed
SQL engine, so lost commit acknowledgement requires live schema inspection.
Only inspected additive schema plus exact candidate runtime acceptance can
commit selection. A separate application journal kind prevents transport-only
recovery from accepting an interrupted schema transaction. Normal menu actions
remain blocked while either transaction is unsettled.

Recovery never replays a database archive or reverses additive DDL. It requires
verified backup/input/image evidence, stopped candidate writers, supported
schema inspection and, if the additions committed, a separate prior-image
compatibility check before restarting the previous application. Unknown or
partial schema, failed compatibility or damaged evidence stays blocked. A
prepared-before-effects crash only verifies the unchanged prior runtime.
Seven local fake-effect tests cover order, scope guards, each interruption,
blocked recovery and retained post-backup data. These are not Docker upgrade,
physical power-loss or production compatibility evidence.

`InspectReviewed` checks exact packaged SQL, exact ledger filenames and the
reviewed additions' columns, types, nullability, defaults, identity/generated
attributes, constraints and indexes against an embedded reviewed reference.
Unexpected rules, user triggers or row policies refuse. It checks the new
objects and connection-default column, not the full historical schema or past
SQL bytes. Both inspection and bootstrap take the shared migration lock with
wait bounds installed before acquiring it. Inspection uses READ COMMITTED so
a wait behind another migrator cannot preserve a stale pre-commit snapshot.
Local PostgreSQL tests verify a real five-second lock timeout without mutation,
a fresh explicit retry, and seeing a concurrent migrator's committed additions
after waiting, plus changed column/default/FK/index/trigger refusals. No
production caller invokes this inspector or coordinator yet.


A separate native-only `test-secure-application-native.py` harness is now being
prepared for real old/new application-image transactions. It builds exact
bfe5d89 API/ingest/MCP/operator binaries and the candidate binaries in temporary
contexts, seeds only a fresh synthetic 89-migration database, and keeps every
network endpoint loopback-only. Its test-binary schema operator accepts only
the explicit synthetic CI database and never ships in tenantctl. The planned
checks include verified unique archive, stopped API/ingest/MCP, durable schema
attempt, structural inspection, exact new-image acceptance and authenticated
retained-data reads, then forced rollback with an old-API compatibility probe
and retained post-backup data, plus a separate successful commit. This harness
has not yet run natively and is not accepted evidence. Its backwards-read
probe remains limited, not full previous-image compatibility or an owner
upgrade. Production adapter/caller, secure Upgrade and owner setup remain open.


Real native application-image transaction evidence is now accepted at c5fb968:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43203826376 .
Both the forced post-new-image-acceptance rollback and separate positive commit
passed using exact old bfe5d89/new API/ingest/MCP images, verified all-writer stop,
unique private archives, structural schema checks and authenticated retained
identity/profile/device reads. Rollback retained additive schema and a DB row
written after backup. This does not promote the limited old-API read probe to
full backwards compatibility or supply an owner production adapter.

Next regression additions are pending native acceptance: index-schema-name
normalization is restricted to the first syntactic indexed-table target rather
than arbitrary SQL text, so schema-like strings in CHECK/default/predicate
values cannot be rewritten into accepted definitions. A PostgreSQL regression
changes allowed state literals to schema-prefixed values and checks refusal.
A third real native fixture path simulates coordinator loss immediately after
reviewed SQL commit but before acknowledgement, checks blocked normal selection,
then uses a new recovery instance and actual schema/old-API probe before exact
prior-writer restoration. It is simulated process interruption, not physical
power loss or a test on the owner's machine. Neither new regression is yet
claimed natively passing.


## Production Docker effect adapter and local schema operator (not enabled)

`secure-application-docker.py` now supplies the actual Docker effect layer for
application-only transactions. It is internal and has no menu/activation caller.
Its fresh private plan performs Docker reads only, pins exact already-local old
and candidate images, classifies API/ingest/MCP, preserves all non-image config,
refuses remote/mismatched DB targets and records stable read-only dependencies.
No candidate operator, pull/build, container effect, journal or SQL is run while
preparing. A missing local image is not permission to fetch it.

The effect adapter requires caller-provided target/downtime/maintenance authority,
retained-data runtime acceptance and previous-image compatibility verifiers.
None has a default success path. The authority verifier must also establish that
outside database writers cannot reconnect during maintenance; Docker inventory
alone cannot establish that fact. The plan is diagnostic, not approval.

Fresh revalidation binds exact container IDs, images and live resolved Compose.
The effect layer creates a unique private pg_dump archive and verifies its list,
stops every classified writer, checks all stopped states and unchanged stores,
and invokes the new candidate image's dedicated local `/bin/setupctl` operator
only within this separate application transaction. It is not an ACL-export
fallback or transport migration. No Docker socket is mounted in any app image.

`setupctl inspect-reviewed` uses the read-only bounded reviewed-shape inspector.
`apply-reviewed` requires an exact effect acknowledgement on stdin, refuses
other current DB client sessions and invokes only the four reviewed additive
DDL/ledger operations, then reads actual shape again. That acknowledgement is
not authority; only the outer caller establishes permission, verified backup
and stopped writers. A client-session check is an extra refusal gate, not proof
that another process cannot reconnect. No startup path invokes either command.
Neither command switches images, changes transport, replays a DB archive or
claims runtime acceptance. The production image now includes this binary.

Local tests pass for malformed acknowledgements before DB access, read-only
baseline inspection, refusal of another live DB client with89ledger unchanged,
then apply/structural inspection after that client closes. Five fake-Docker
adapter guards cover callback requirements, local DB targeting, changed/private
plan refusal and MCP-still-running refusal before any schema operator. The real
native old/new image fixture has been rewired to use this production plan/effect
layer and setupctl while retaining synthetic-only authority/acceptance probes.
That new integration is pending native execution. The earlier d710ad9 evidence
used the synthetic adapter, not this new production layer. No owner installation
has changed, and no owner-ready upgrade/menu path is enabled.


Production effect-layer acceptance passed at12b2995, all three native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43206981266 .
The raw Linux log confirms all three real old/new application paths used the
production Docker adapter and packaged setupctl, not the earlier synthetic
schema effect. Authority, retained-data reads and previous-image compatibility
callbacks were still synthetic fixture verifiers. This is no substitute for
the unfinished owner-facing caller, maintenance/external-writer boundary or
full old/new application compatibility checks. No owner change occurred.

Next local hardening refuses DB URL query parameters that can override the
hostname/database or supply custom schema/options, and duplicate parameters.
The allowed query keys are sslmode, connect_timeout and application_name only.
This closes a way to pass a local hostname check yet connect elsewhere. The
backup database is passed as an explicit --dbname value, not an option-like
positional string. These DB-target guards passed all three native jobs at89e0776, with all
three production adapter/setupctl paths read in the raw Linux log.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43208081436 .


Actual-container binding is accepted at8a02b7f:
preparation and runtime checks compare Docker's real command, entrypoint and
full effective environment (image defaults plus resolved service overrides),
not just desired Compose text and image ID. Duplicated/extra/changed environment
or changed command/entrypoint refuses, as do actual privileged/host-network
containers. Partial recovery accepts only planned old/new images and checks
those actual definitions before stopping anything; unchanged stores must be
present. This prevents stale or manually altered containers from redirecting
a same-image transaction to a different database. Seven adapter guards and all three production adapter/setupctl transaction
paths passed natively. v19 initially failed read-only preparation because
Compose emits null command defaults. v20 fixes null inheritance while explicit
empty commands still clear. Raw synthetic image/actual metadata confirmed the
normal broker/Postgres/web defaults.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43210192809 .
Both test branches were deleted and API404 checked; main was unchanged.

Next local guard also refuses planned application-writer entrypoint wrappers
and libpq PG* environment overrides rather than accepting them merely because
actual matches planned. Actual definitions are revalidated immediately before
any schema operator. Eight adapter guards and all three real production adapter/setupctl paths
passed atb360a45 in all three native jobs; raw Linux log read.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43211137471 .
Disposable v21 deleted/API404 verified; main unchanged.


Next acceptance work replaces substring checks with exact retained-list JSON
comparison for gateway identity/status/site, device association/metadata and
profile names/driver/points. The only normalized additive field is the reviewed
empty connection_defaults value introduced by migration0090; a nonempty change
refuses. Missing/extra/duplicate rows or fields and list caps refuse. This
reusable pure comparator has no transport, authority or full-runtime success
and does not make network requests. Native fixture now seeds nonempty points
and a complete synthetic source configuration and checks exact DB JSON after
each path, plus unauthenticated401 and other-tenant empty-list behavior against
old/new APIs. These exact-list/source-config and auth/isolation checks passed all three
native jobs at3b78163. Raw Linux logs confirm all three production application
rollback/commit/lost-ack recovery paths passed; v22 deleted/API404 verified.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43212302289 .
They still do not
prove all application writes/background behavior, meter readings or owner
maintenance/external-writer prevention.


The first internal production transport Docker effect adapter is now local:
secure-transport-docker.py. No CLI or owner/menu caller is supplied. It requires
explicit authority and runtime acceptance callbacks; those must establish the
actual route, trust, downtime/maintenance boundary and observed HTTPS/broker/
gateway behavior, never a receipt. It binds the exact supported rendering,
prior/current Compose, immutable images, actual environment/commands, fresh
container IDs and TLS files before downtime. All classified API/ingest/MCP
writers stop before the exact current-image full ACL export; changed ACLs
require a new preparation rather than using a stale snapshot. Backup is a
private unique verified pg_dump archive. ACL staging is atomic with the selected
shared group. Recovery accepts planned old/new definitions only, restores
exact prior runtime and never replays the database. Five pure guards and fresh
full server suite pass. Native forced-rollback/positive-commit fixture now uses
these production effects with synthetic authority/runtime callbacks; all three native jobs passed at17de822. Raw Linux log confirms both production
transport rollback/positive-commit paths and all three production app paths.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43213107015 .
Disposable v23 deleted/API404 verified; main unchanged. No owner activation, full compatibility or post-pair
observed authorization claim is made.


Next native work explicitly tests pairing after that same actual TLS deployment
commits. It compiles edge into the test image, retains a fresh synthetic saved
key/JWT/config, requests through real HTTPS and uses real administrator approval
of exact fingerprints. With ingest stopped for one attempt, a missing nonce
observation must time out without installing a certificate or changing saved
identity/config. Restart plus explicit retry must obtain a fresh own-gateway
broker/ingest/PostgreSQL receipt before install, with actual ACL checksum change
and watcher reload, then foreign-tenant retained-publish absence using the new
certificate. The helper is restricted to the hardcoded disposable CI fixture,
never an owner CLI. This new integrated post-pairing outcome is pending.
Production transport revalidation also now explicitly checks current operator
protocol/schema/writer inventory before effects; six local transport guards
pass. No source starts, meter reads or owner trust installation occur.


Integrated native v24/v25 reached real HTTPS administrator approval, which
returned409: configured CA cannot sign this request. The fixture's OpenSSL3
ECDSA CA key is PKCS8, accepted by TLS/preparation but rejected by the old
SEC1-only signer. Local signing now uses matched tls.X509KeyPair parsing for
SEC1/PKCS8 ECDSA keys, refuses mismatched/non-ECDSA/non-signing/expired issuers
and nonpositive client lifetimes; caps issued lifetime at the CA expiration. Focused PKCS8 success and mismatched-key/
lifetime refusal regressions pass. This corrects a signing defect without
changing approval/authentication rules. Integrated native success remains
pending. v24/v25 test branches were deleted/API404 verified; main unchanged.


Follow-up local hardening aligns private preparation and transport revalidation
with the actual signer: matching ECDSA CA, explicit certificate-signing use,
current validity and supported NIST curve. Checks inspect only certificate/public
key data and compare derived public keys; no supplied key conversion, new issuer,
or CSR signing. Synthetic preparation tests accept PKCS8 P-256/P-384/P-521 and
legacy SEC1, refuse RSA/unsupported curve/non-CA/wrong usage/mismatch, and prove
key bytes unchanged. Transport issuer recheck must succeed before effects.
Native acceptance for this follow-up is still pending.

Integrated same-deployment pairing acceptance passed at b3beb03, all three
standard native jobs green, full raw Linux evidence inspected:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43216665200
Real HTTPS exact-fingerprint approval now signs the OpenSSL3 PKCS8 CA key.
The compiled saved-key edge on the actually committed TLS deployment refused
certificate installation with ingest stopped and preserved identity/key/config;
explicit retry obtained a fresh own-gateway nonce observed by broker/ingest/DB
before installation. Full ACL replacement/watcher reload and new-certificate
foreign-tenant retained-publish absence passed. Both production transport paths
and all three application paths passed again. Temporary branch deleted/API404,
main unchanged. This is synthetic deployment evidence, no owner activation,
source start, physical reading, full compatibility or owner-ready update.
The newer CA preparation/revalidation alignment remains pending native testing.


CA preparation/revalidation alignment at57b800d passed all three native jobs;
raw Linux output confirms five preparation/seven transport/seven transaction
guards and repeated same-deployment pairing, both transport and all three
application transaction paths. Temporary v27 branch deleted/API404 verified,
main unchanged. https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43218567299

Next native extension is pending: after pairing on that same activated TLS
fixture, start the compiled edge against a fresh synthetic-only Modbus TCP
FC03/register10/unit1/count1 simulator. Review sources through actual HTTPS,
refuse stale fingerprint without an apply row, apply the signed exact reviewed
source, require independent broker/ingest confirmed result and fresh tagged
synthetic value110 in PostgreSQL/HTTPS series, and prove same-process reload
plus unchanged original saved identity/key/certs/config. Discovery, scans,
outputs, remote restart and commands remain disabled; unexpected simulator
requests fail. Three scope/protocol guards pass locally. No physical meter,
owner activation, full compatibility or native success is claimed yet.

Same-deployment source extension accepted at588fa54, all three native jobs
green and full raw Linux log inspected:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43220947979
The actual activated-and-paired TLS deployment passed real HTTPS exact source
review, stale fingerprint refusal without an apply row, signed apply to the
compiled edge, independently ingested confirmed result, live same-process poll
reload and tagged synthetic Modbus TCP FC03/register10 value110 in PostgreSQL
and authenticated HTTPS series. Original saved identity/key/certs/config
remained byte-identical. Simulator only, no owner activation, discovery, scan,
device output command or physical meter evidence. Pairing receipt/ACL reload/
new-cert foreign-publish absence, both transport and all three application
paths passed again. Temporary v28 branch deleted/API404 verified, main unchanged.
Owner execution caller, maintenance/outside-writer controls, full application
compatibility, secure Upgrade and physical USB/decoder/reading remain gates.


Follow-up native source save/resume extension is pending: replace synthetic
profile/device SQL seeding with actual HTTPS profile creation, existing-gateway
source session and validated profile assignment. Invalid connection must refuse
without saving; duplicate save refuses; a fresh GET must recover exact saved
connection; preview stays not-live before explicit apply and shows the recent
synthetic sample only after signed apply/confirmed/reading. Fixture-only cleanup
of seedDemo sample rows remains SQL, scoped before all source actions. Four
scope/protocol/returned-ID guards pass locally. No serial port test, physical
USB/decoder, owner activation or native success is implied by this addition.

Source save/resume extension accepted at274c5dc, all three native jobs green;
full raw Linux output inspected:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43223245291
Actual HTTPS profile/source-session/save validates the connection, refuses
invalid/duplicate save and returns exact saved connection on resume. Preview
stays not-live before explicit apply; signed apply/independent confirmation/live
poll reload produces the tagged synthetic110 sample in DB/HTTPS/recent wizard
preview. Original saved identity/key/certs/config unchanged. Pairing, both
transport and all three application paths passed again. v29 branch deleted,
API404 verified, main unchanged. Simulator only, not a serial port test,
physical meter/USB/decoder, owner activation, full compatibility or rollout.


### Read-only recovery review (native v30 accepted)

The terminal menu now has "Review interrupted setup" and the command
`hexthings secure-recovery-review`. This is a review, not an execution or
approval button. It does not call Docker, create state, write bytecode caches,
change a selector, restore a database or clear an interrupted journal.

It takes the existing operation lock without creating one. Interrupted TLS
and application transactions stay blocked. The review checks private previous
runtime files, their full retained-input manifest, exact recorded image IDs
and the private backup checksum when service changes may have begun. These
are file checks, not local-image, archive-restore or live-runtime acceptance.
Application recovery also requires unchanged candidate inspector inputs; TLS
review can identify damaged candidate files while retaining valid prior inputs.
A settled selector is checked separately and is never called healthy merely
because its saved files match. Corrupt state and changed dependencies refuse
review without printing their contents.

Nine local test groups cover every interrupted phase in both scopes, settled
selectors, damaged prior/candidate/backup inputs, held/missing/shared/symlinked
locks, CLI redaction and refusal of execution flags, shell routing without
Docker, unchanged file bytes/modes/mtime and 80-column safety wording. The
terminal menu PTY contracts and existing state/transaction/application adapter
guards pass locally. Native acceptance is still pending for this addition.

Owner recovery execution remains unavailable: current installation authority,
maintenance/outside-writer prevention, actual image/service checks, live schema
inspection and prior-image compatibility where needed, route/trust/broker
acceptance and retained-data acceptance must be supplied before mutation.
This change does not close physical USB/serial port-test/Schneider decoder or
MQTT-source gaps, or make secure Upgrade ready for the owner.


Recovery review25ace3f accepted on all three native jobs; raw Linux evidence
inspected, including the new nine review contracts, repeated same-deployment
HTTPS source save/resume/apply/confirmed/synthetic110 preview, both transport
paths and all three application transaction/recovery paths:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43226732332
Temporary v30 branch deleted at exact SHA/API404 verified, main unchanged and
copied credential removed. This supersedes the pending-native wording above
for this addition only. Recovery execution remains unavailable, no owner-ready
update, full compatibility or physical USB/serial/decoder/MQTT-source claim.

### Execution/recovery coordinator (native acceptance pending)

`secure-execution.py` now supplies the internal caller, not only a review.
`Coordinator.run(root, project, receipt, action)` routes activation/recovery to
production Docker adapters and the matching transport/application engine. The
constructor requires a current scope reviewer, a maintenance-session provider,
live runtime acceptance and previous-image compatibility verification. There
is no default provider, saved approval flag or owner menu activation shortcut.

One OS lock spans review, maintenance, effects, selection and release. The
maintenance provider must actually prevent outside database writers reconnecting;
counting pg_stat_activity or stopping only Compose writers does not satisfy it.
An opaque session and the exact action/project/config/receipt digest are bound
for the invocation. Every adapter entry is checked before and after, including
backup, schema, runtime acceptance and rollback. Verifiers raise on failure;
returning True or False instead is rejected rather than silently accepted.

The journal records maintenance held before service changes. Normal selected
operations refuse a held marker even on a committed/restored runtime. Process
loss, fence loss or release failure retains the block. A separate reviewed
recovery of an already settled transaction rechecks exact inputs and reaccepts
its recorded runtime before releasing maintenance, without repeating schema
application or blindly replacing a committed deployment. Automatic rollback
under a lost fence is refused. No database archive is replayed.

Eight local coordinator test groups cover positive activation, missing scope/
provider/approval flags, fence loss after SQL, process-loss recovery with rows
retained, release failure after commit, changed recovery receipt, independent
transport routing and pre/post checks. Ten review groups include a settled
runtime with maintenance release pending. Existing state/application/transport
adapter guards pass. The real disposable native application fixture now invokes
this caller for forced rollback plus explicit settlement, positive commit and
lost-SQL-ack recovery, with a hard CI-scoped synthetic maintenance provider.
Native acceptance is pending. This provider is not production outside-writer
prevention or owner authority, and retained-list compatibility is still limited.

Remaining execution work: host-specific maintenance provider, full write/
background retained-data acceptance, previous-image compatibility, owner route/
trust/downtime review, menu connection and safe secure Upgrade. Physical USB,
serial port testing, Schneider decoder and MQTT-source gaps remain separate.


Coordinator admission hardening is a separate pending-native follow-up: persist
a private maintenance sidecar before invoking the provider, including when no
transaction journal exists yet. An interrupted admission remains blocked until
a fresh review/session verifies the exact previous runtime and releases the
fence. This path runs no transaction backup/schema/start/stop. The production
adapter's locked pre-change inspection uses the captured verified selector,
not a general ordinary-action bypass. The sidecar is removed only after release
and durable journal settlement; interrupted removal remains recoverable.
Nine coordinator groups pass locally. The native fixture now adds admission
process loss, unchanged current container IDs, blocked selection and fresh
no-service-change recovery before its three application paths. v31 does not
include this admission hardening. No production fence or owner-ready claim.

Execution caller4f3e43c/v31 passed all three native jobs and raw Linux evidence
was inspected: eight caller/ten review contracts, all three real production
application paths through the new caller, retained post-backup rows and held/
released lifecycle checks, plus repeated same-deployment pairing/source110 and
both transport paths. https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43231389824
v31 temporary branch deleted at exact SHA/API404, main unchanged. Admission
hardening above is newer local work, not covered by this accepted run. Its real
native fixture also adds a baseline89-only runtime acceptance for the pre-schema
admission recovery, rather than incorrectly requiring additive93 before changes.

Admission hardeningdd4ea48/v32 accepted all three native jobs, raw Linux log
inspected. Nine caller/ten review groups and the actual pre-journal admission
loss/blocked-selection/fresh baseline89 acceptance/no-service-change settlement
passed. All three real application coordinator paths, same activated/paired TLS
HTTPS source save/resume/apply/confirmed/synthetic110 preview and both transport
paths passed again. https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43232400111
v32 branch deleted at exact SHA/API404 verified, main unchanged and copied
credential removed. This supersedes pending-native wording for admission only.
The outside-writer maintenance provider is still synthetic, not production
fencing, owner authority, full compatibility or an owner-ready update.

### Exclusive PostgreSQL admission component (local tests; native pending)

`secure-postgres-fence.py` implements an internal host component, not the complete
maintenance provider and not a menu activator. An exact immutable PostgreSQL
container and explicit application database/admin identity are required. It uses
a separate `postgres` catalog connection. It closes target-database admission
with `ALLOW_CONNECTIONS false`, then terminates existing target sessions. Both
ordinary users and superusers are denied new target-database connections. Merely
counting sessions or stopping Compose is not substituted for this gate.

The deployment OS lock and matching coordinator admission marker are required
throughout. A private durable record binds the exact reviewed transaction,
cluster system identifier, database OID/owner and original admission state before
ALTER. Identity validation and catalog mutation share a bounded catalog-lock
transaction, avoiding a name-only drop/recreate race. A preclosed database is
refused rather than adopting responsibility for reopening it. Replication slots,
subscriptions, prepared transactions, templates, standby databases and absent
admin authority are refused. The explicit fresh scope reviewer still supplies
authority; a record is not approval.

Admission loss before acknowledgement leaves a recoverable closing intent.
Reopening records releasing first: a lost reopening acknowledgement can mean the
database is already open, not that writers remain excluded. Ordinary selected
operations remain blocked by the enclosing maintenance marker. Explicit fresh
recovery recloses and drains that exact database before further effects, without
replacing the original baseline or replaying an archive. Released evidence is
kept for the enclosing settled transaction to archive; this component never
clears the coordinator marker. Fence/identity loss refuses effects and release.

Eight pure local groups and one real disposable PostgreSQL14 group passed:
existing writer terminated, superuser and ordinary-role reconnect denied, another
database's live session preserved, retained rows and both lost-ALTER-ack recovery
paths checked. Production SQL was executed locally, but that is not Docker proof.
The native application fixture now adds the same proof through actual
`DockerCatalog` against a separate throwaway PostgreSQL16 database before app
writers start. Native acceptance is pending.

Integration limit: this exclusive interval admits NO new target connections,
including migration and application acceptance connections. It therefore is NOT
yet wired as Coordinator's default provider. A controlled maintenance channel
and fenced handoff to accepted application writers remain to be implemented.
It also does not prevent an independently privileged catalog administrator from
changing admission or a host administrator from replacing the container; host
inventory/ingress restrictions must bound those capabilities. No full provider,
owner activation, full write/background compatibility, secure Upgrade, physical
USB/decoder or MQTT-source acceptance is claimed.

PostgreSQL behavior references:
https://www.postgresql.org/docs/current/sql-alterdatabase.html
https://www.postgresql.org/docs/current/sql-createdatabase.html
https://www.postgresql.org/docs/current/functions-admin.html

Exclusive database component5af31b3/v33 accepted all three native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43236924917
Full raw Linux evidence retrieved and checked, including actual DockerCatalog
on PostgreSQL16: old writer terminated, both superuser/ordinary reconnects denied,
unrelated database live session preserved, exact retained rows and both real
ALTER lost-ack recovery paths. The optional local PostgreSQL14 test is skipped
in CI; this separate Docker fixture passed. Existing three application caller
paths, admission recovery and same paired HTTPS source110 flow passed again.
Exact v33 temporary branch deleted/API404, main unchanged and copied credential
removed. This supersedes native-pending wording for this component only, not the
controlled connection/accepted-writer handoff/host-inventory gap described above.

### Controlled local maintenance and exact-peer host-auth barrier (local; native pending)

`secure-postgres-hba.py` now implements a recoverable first-match HBA barrier.
It preserves the existing local admin authentication, rejects other local users
and all target-database network clients, then optionally admits only exact
reviewed IP/user/SSL peers whose original first-match rule already requires
SCRAM. Trust, md5 or a prior reject never becomes an application permission.
Unknown includes, groups, DNS, options, quoting and continued rules refuse rather
than being rewritten. Original authentication for other databases is unchanged.
Atomic host-file replacement preserves owner/mode and fsyncs file/directory.
Private staging precedes replacement. Interrupted handoff/release requires fresh
recovery to maintenance-only access; changed files/scope or missing inventory
are not silently overwritten. Released evidence and coordinator admission remain.

`secure-postgres-hba-docker.py` binds that file to the running immutable
PostgreSQL container/start/image and server-reported bytes through one canonical
writable local Docker directory volume. Bind/remote/file mounts, redirected
paths, ambiguous mounts, privileged/host-network containers and parse errors
refuse. Exact candidate peer container IDs, service/project and current shared
network/IP are rechecked, not inferred from DNS or a reusable address. The host
caller still owns candidate/previous runtime authority and complete host ingress/
socket inventory. A separately privileged host/catalog administrator is outside
this boundary, not contained by an HBA file.

Reload acknowledgement alone is insufficient: server reload time, syntax,
unchanged bytes/inventory and caller-supplied fresh positive/negative connections
must all pass. Local PostgreSQL14 fresh-process proof passed: controlled local
admin remained usable, unlisted network user rejected, exact SCRAM peer admitted
only at handoff, lost real reload acknowledgement recovered to closed access,
original bytes restored. Five pure HBA and four Docker-inventory groups passed.
Native fixture now additionally combines this actual volume-backed HBA with the
exclusive database close/drain/reopen, proving local SQL remains usable behind
closed network admission and fresh recovery recloses an interrupted peer handoff.
Native acceptance is pending.

This is NOT yet the complete Coordinator provider. Existing Docker application
operators still use network connections, and candidate/previous app startup and
compatibility probes need explicit scoped handoff integration. Controlled-channel
routing, full retained write/background acceptance, owner route/trust/downtime
review, menu/secure Upgrade and physical USB/decoder/MQTT-source checks remain.
No owner system, credential, main branch, release or ordinary app route changed.
HBA first-match/reload behavior:
https://www.postgresql.org/docs/current/auth-pg-hba-conf.html

Controlled HBA component25a0c93/v34 accepted all three native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43239962368
Full raw Linux evidence checked: actual immutable container/volume/server-byte
binding, preserved local admin channel, exclusive drain/reopen behind closed HBA,
unlisted IP/user denied, exact SCRAM peer handoff, real reload-loss recovery
reclosed access, retained89/93 and original HBA restored. Five pure HBA and four
Docker guards passed; optional local-process PG14 test was skipped in CI, with
separate actual Docker PG16 proof passing. Admission recovery, app three paths
and same paired HTTPS source110 flow passed again. Exact v34 branch deleted/API404,
main unchanged and copied credential removed. Supersedes native pending only
for this component, not application operator/compatibility routing or complete
host-provider/owner readiness.

### Controlled local schema-operator callback (local; native pending)

`DockerApplication` now accepts an explicit `maintenance_operator` callback.
It is used only after the existing all-writer-stop verification, while apply
still requires verified backup and durable exact schema-attempt evidence. No
callback silently disables the original route or claims maintenance authority.
A host provider must supply the controlled callback when holding its HBA fence.

`secure-postgres-operator.py` stages the exact candidate setupctl executable and
migration catalog from a never-started immutable-image container with no network.
Only bounded regular ELF/SQL entries are accepted, with traversal/link/unknown
paths refused. Private durable source/destination/hash state precedes effects.
The target path is unique and mode700; every file is hashed before invocation.
Commands are checked before and after against the caller's live fence/inventory.
The executable runs in the exact PostgreSQL container with a cleared environment,
explicit existing database/admin identity and the server-reported single Unix
socket directory. Custom/multiple/abstract socket paths refuse, without TCP
fallback, new role or password. Schema/catalog drift still refuses in setupctl.

Four local contract groups passed, including adapter callback ordering, archive
path/type refusal, immutable staging, exact local DSN, cleared environment,
changed scope/socket/tool/fence refusal and no fallback. Existing adapter guards
passed. Native fixture now clones its fresh baseline89 database before app
writers start, then invokes actual candidate setupctl under the volume-bound HBA
barrier: inspect89, verified archive, retained post-backup row, apply93, simulated
lost acknowledgement, fresh inspect93 without replay, retained identity/row,
checked staged-tool cleanup and original HBA restoration. Native acceptance pending.

Interrupted partial staging remains blocked for explicit recovery; this module
never deletes unknown partial directories or image containers as exception
cleanup. This is not complete host-provider integration: candidate/previous
application writers, compatibility probes and full write/background acceptance
still require scoped handoff. No owner activation/menu release claim.

Controlled local setupctl component73f1fda/v35 accepted all three native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43242495366
Full raw Linux evidence checked: exact candidate setupctl/catalog extracted from
never-started immutable image, hashed staging, closed target HBA rejecting outside
network, actual local socket inspect89/apply93, lost SQL acknowledgement followed
by fresh inspect93 without replay, post-backup row and gateway retained, checked
tool cleanup and original HBA restored. Existing exclusive/HBA proofs, app three
paths and same paired HTTPS source110 passed again. Exact v35 branch deleted/API404,
main unchanged and copied credential removed. New local partial-tool recovery
and more direct changed-scope/socket/tool guard tests are not in this accepted
run; they require their own native proof. Application runtime/compatibility
handoff and complete host-provider readiness remain unfinished.

Partial local-tool staging recovery is now a separate local follow-up:
`recover_staging()` accepts only a recorded pre-SQL creating/copying state under
the same locked admission/scope. The source must still be the exact never-started
image/label. Any target directory must be private mode700, root-owned and contain
only known regular candidate files with matching hashes. Unknown/changed files,
links or a running/replaced source refuse. Only verified transient tools are
removed, evidence is archived durably and admission remains held. No SQL is run.
Five local groups pass, with changed-scope/socket/tool tests now exercising inspect
directly instead of failing apply's earlier backup prerequisite. Native now adds
lost acknowledgements after actual mkdir and copy, fresh checked reset, unchanged
baseline89 schema and two retained evidence records before the accepted local
operator proof. Native acceptance for this follow-up is pending.

Partial tool recoveryda88c98/v36 accepted all three native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43244701123
Full raw Linux log inspected (263930 characters). Both actual mkdir/copy
lost-acknowledgement recoveries passed: fresh scope/source/hash/private-path
verification, tool-only reset, two archived evidence records and unchanged89
schema while HBA remained held. The actual local candidate setupctl89->93,
lost SQL acknowledgement/fresh inspect without replay, post-backup row/gateway
retention and original HBA restoration passed again. Earlier exclusive/HBA
proofs, three synthetic-provider app paths and same paired HTTPS source110
passed. Exact temporary v36 branch deleted/API404, main unchanged and copied
credential removed. This supersedes only the partial-tool native pending label;
full application/compatibility handoff and owner readiness remain unfinished.

### Dynamic-endpoint application process gate (local; native pending)

The old API exits on its first denied PostgreSQL ping, while Docker dynamic
addresses are assigned at start. Starting it and later opening HBA is not a safe
handoff. `secure-application-startgate.py` now builds a narrowly checked direct
API/ingest/MCP shell wait with a unique read-only bind. The real application is
not executed until the host caller has bound the actual running container and
proved its exact peer admission. No guessed address, subnet allow, new credential
or changed database target is used. The timeout fails closed after five minutes.

Private held/releasing/released evidence precedes effects; the application binary
is exec'd as PID1 with its original argument array, without eval. Exact immutable
image, command, environment, project/service/nonce labels, read-only mount set,
container ID and unprivileged running state are checked. Only existing read-only
file binds are supported. Unknown marker files, symlinks, changed bytes/mode,
missing released marker, scope changes and unexpected runtime definitions refuse.
A lost marker acknowledgement requires fresh inventory and peer proofs without
removing the marker or restarting/replaying the application. Pre-launch directory
preparation has its own explicit recovery and cannot reset a launched process.

Six local contract groups passed, including a real shell no-exec-before-marker
and literal-argument check, changed identity/command/env/mount/capability refusal,
failed/boolean admission proofs, changed marker and fresh lost-ack recovery.
Native adds a separate baseline89 clone with the actual exact prior API image:
start held shell, inspect assigned endpoint, prove that same endpoint HBA-denied,
allow exact already-SCRAM peer while another IP stays denied, lose actual marker
acknowledgement, recover with fresh peer proof, verify actual API PID1/health and
retained gateway/history, then restore original HBA. Native acceptance pending.

This remains a startup component, not a complete host provider. Full application
adapter grouping, candidate/previous/compatibility gate lifecycle, final selector
and later-update reconciliation, outside ingress/inventory bounds and full
write/background compatibility must be wired before owner execution. Recovery
must stop and drain any possibly released application before further schema
changes; a retained release marker is not evidence of stopped writers.

Startup nativef8927b6/v37 was NOT accepted: both Windows jobs passed, Linux
failed at the final fixture restoration callback. Actual prior API first-DB
holding, exact peer handoff, lost marker acknowledgement/fresh proof, API PID1,
health and retained89/gateway checks reached completion. The callback incorrectly
required original-open access while Barrier.release() correctly rechecked the
still-closed pre-release HBA. The callback now proves closed access until exact
original bytes are installed. No product gate was relaxed. Failure evidence:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43248144012

### Grouped startup and final direct-runtime reconciliation (local; native pending)

The application adapter now accepts an explicit `runtime_group` manager, never
a default bypass. Actual gated definitions require its exact recorded verifier;
standard direct-image/command/environment checks remain the default. The group
creates all API/ingest/MCP containers, durably binds their exact created IDs
before start, gathers their real running endpoints, invokes one explicit exact
peer handoff and requires fresh grouped proofs before each process release.
Separate candidate/previous/compatibility slots cannot share markers. Old gates
can only be retired after fresh exact stopped-container verification; all prior
marker/directory evidence remains and a new attempt gets a new nonce. Interrupted
start can recover the same bound held IDs, not silently replace them.

Final normalization is a separate post-settlement step. It requires a committed
or restored application journal, exact original file manifest/config hashes and
proof that original HBA bytes/authentication are restored. A durable normalization
record precedes recreation of the selected direct commands/mounts. The ordinary
strict adapter verifier and live acceptance run again before the caller may remove
the maintenance marker. No schema operation occurs during normalization. This
keeps transient wrappers out of the final selector and next-update expectations.

Eight local gate groups, five grouped fake-Docker groups and existing adapter
guards pass. Native now adds actual prior-image API/ingest/MCP against a separate
baseline89 clone, with all real processes gated before DB, grouped exact endpoint
handoff/outside denial/fresh per-release proofs, actual PID1/health/history checks,
then original-HBA restoration, direct-runtime normalization and strict inspection
without a gate manager. Its settled journal is an explicit fixture, NOT a real
Coordinator commit or complete host-provider proof. Native acceptance pending.
Full provider inventory/ingress, compatibility-probe routing, real release/lost-
release recovery and owner/menu integration still remain unfinished.

Grouped startup6dd3c73/v38 was NOT accepted. Both Windows jobs passed but Linux
stopped in the older transport fixture before reaching the new application
native proof: DockerTransport borrows checked_containers from DockerApplication,
and the new method called an application-only verify_definition method absent
on that transport object. The correction supplies an explicit transport-only
verifier retaining direct definitions and refusing all process-gate labels.
A regression now exercises shared checked_containers on transport and verifies
that an unreviewed application gate still refuses. No runtime check is skipped.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43249919464

Grouped startup4f85465/v39 was NOT accepted. Both Windows jobs passed; the
individual actual prior-API gate now passed through original HBA restoration.
The grouped fixture's initial Compose create recreated dependencies and replaced
the exact PostgreSQL container, which the immutable maintenance inventory guard
correctly refused. Both grouped create calls now specify --no-deps; the local
group contract rejects dependency recreation. No inventory requirement relaxed.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43251819574

Grouped startup84a57d9/v40 was NOT accepted. Linux rejected the grouped fixture's
Compose create invocation before startup; both Windows jobs passed. Current
Docker documentation confirms create has no --no-deps option. The supported
no-start/no-dependency path is now `compose up --no-start --no-deps --no-build
--pull never`, preserving the created-ID-before-start boundary without dependency
recreation. The fake contract now models that actual command instead of assuming
an unsupported create flag. This correction does not relax inventory or admission.
Sources: https://docs.docker.com/reference/cli/docker/compose/create/
https://docs.docker.com/reference/cli/docker/compose/up/
Failure: https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43253849288

Grouped startup64bf033/v41 was NOT accepted. Linux reached actual grouped
created-container verification and refused a different Cmd; both Windows jobs
passed. The grouped Compose recipe must escape literal dollar expressions in
the shell script with $$. The gate's expected actual Docker command stays literal
and unchanged. Only serialized Compose recipe script dollars are escaped; actual
Cmd equality remains strict. The fake group now models Compose interpolation and
requires the escaped recipe, rather than treating recipe and actual Cmd as equal.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43256012106
Current Docker interpolation reference confirms $$ preserves a literal dollar:
https://docs.docker.com/reference/compose-file/interpolation/
The next native fixture emits exact non-secret gated Cmd plus literal-script
equality for each real created writer; this verifies the suspected interpolation
cause rather than treating the prior mismatch alone as proof of its cause.

Grouped startup39d42d6/v42 accepted all three exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43258234193
Full raw Linux evidence inspected (266521 characters, 2957 lines). All three real
created gated Cmd values report literal_script_equal=true. Actual prior-image
API/ingest/MCP ran through grouped exact dynamic endpoint SCRAM handoff, outside-IP
denial, fresh proofs per release, actual PID1/health/baseline89, fixture-settled
original HBA restoration, direct command/mount normalization and strict final
inspection without a gate manager. Individual old-API marker-loss/fresh-proof
recovery and earlier exclusive/HBA/local-operator/partial-tool proofs passed;
transport two paths, app three paths and same paired HTTPS source110 repeated.
Exact v42 branch deleted/API404, main unchanged and copied credential removed.
This supersedes grouped native pending, not the explicit fixture-journal limit:
real Coordinator/provider lifecycle, compatibility routing and release recovery
remain unfinished. No owner/menu activation, full compatibility or hardware claim.

Concrete host-provider integration (local, native acceptance pending):
`secure-application-provider.py` composes the exact Coordinator-held journal,
reviewed PostgreSQL CID/image/database/admin and server volume HBA binding,
maintenance-only HBA plus exclusive catalog close/drain/reopen, candidate static
local-socket operator, grouped dynamic-peer admission and a distinct previous
compatibility group. Pre-stop admission is closed before stopping admitted peers.
Durably settled release recovery preserves the original HBA baseline, then
reconciles direct runtime definitions and accepts them before ordinary selection.
Explicit fresh connection/scope/runtime/compatibility callbacks remain required;
no menu activation or implicit authority. The new native fixture runs through
real Coordinator commit/rollback and injects lost original-HBA-release
acknowledgement, checking no SQL replay. Until its exact-tip full raw evidence is
accepted, this is not a successful lifecycle proof. Local provider6, execution10,
group6, adapter8, HBA/operator/catalog contracts and full fresh-DB server suite
pass. Owner full compatibility, outside privileged-host/local-admin access,
transport activation, hardware and ready-to-update acceptance remain exclusions.


Concrete provider v43 6e534a7 NOT ACCEPTED:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43262472834
Both Windows jobs passed; Linux cancelled at its existing 15-minute boundary.
Full raw257186characters/2878lines inspected: earlier actual grouped component
passed16:52:38Z, then no new provider milestone before17:02:40Z cancellation.
The precise provider stage remains unresolved. This is neither a proved guard
failure nor successful provider lifecycle acceptance. Exact branch deleted204,
API404/main unchanged/copied token removed. Next fixture adds bounded subprocess
timeouts, stage/elapsed/command counts and periodic traceback diagnostics; runs
the integration before repeated operator/gate/group proofs without changing the
job budget or any inventory/admission guard. Exact preflight digest binding also
has a negative local regression. No owner activation.

Diagnostic v44 efed241 overall NOT ACCEPTED (Linux15-minute cancellation):
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43266536493
Both Windows jobs green. Full raw294840characters/3184lines inspected. The new
actual concrete provider/Coordinator fixture itself PASSed17:24:42Z, before
17:25:28Z cancellation in following component work. Dedicated previous-image
compatibility285s; previous acceptance421s; original-HBA/direct-normalization
release457s; second actual commit followed by original-HBA lost acknowledgement,
fresh recovery605s, direct candidate normalization626s and release627s. Exact
post-backup row retained, no SQL replay and final direct PID1/health/image checks.
This is scoped positive fixture evidence, not a full green native acceptance or
owner/full-background-write compatibility. Exact branch deleted204/API404/main
unchanged/token removed. Diagnostics show10900 fresh Docker calls, much nested
peer inventory. Next local change batches PG+all exact peer CIDs in one fresh
Docker snapshot per guard, never caches across guards/effects, rejects missing,
duplicate/extra IDs and checks bound PG image/start and every peer address/network.
Host/server/volume check remains before snapshot. Local regression5 passes.

Concrete provider v45 82be797 ACCEPTED all3 exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43270435725
Full raw300041characters/3205lines inspected. Actual provider/Coordinator proof
passes: exact PG catalog/volume/HBA; baseline89->additive93 via static candidate
local socket operator; API/ingest/MCP dynamic gated exact-SCRAM handoff with
outside-IP denial; forced candidate failure -> dedicated previous-image gated
compatibility group -> exact previous runtime; post-backup row retained; actual
committed journal followed by original-HBA-release acknowledgement loss; fresh
settled recovery without apply-reviewed replay; original HBA plus direct final
Cmd/PID1/images/health without gate manager; ordinary selection removed last.
Then prior operator/gate/group, both transport paths and synthetic Coordinator
activation/recovery paths pass. Provider542s/6258calls versus v44's627s/10900calls;
batching keeps fresh guard boundaries with no cross-guard cache or relaxed checks.
Exact branch deleted204/API404/main unchanged/token removed. This is not owner
activation, secure Upgrade/menu or full background/write compatibility/hardware
acceptance. v43/v44 overall failures remain recorded; v45 is the accepted tip.

Provider v46 local write compatibility extension, native acceptance pending:
The separate disposable provider project now seeds a dedicated synthetic
write-tenant, real admin/viewer records, gateway/device and bounded voltage point.
The retained source/identity tenant remains untouched. A fixture-only probe checks
profile creation and exact saved-list readback; real authenticated HTTP telemetry
write/latest/count; explicit event retry without timestamp deduplicates; range
rejection and unauthenticated/viewer/other-tenant denial leave no telemetry rows.
It retains all synthetic profiles/events across candidate, dedicated previous
compatibility, previous restoration, settled recovery and final direct runtime.
No production mutation probe, owner menu action or new authority is introduced.
Local old89 -> candidate93 -> old93 -> new93 real API processes pass four stages,
with four profiles/four deduplicated events retained. The initial local viewer
fixture incorrectly used the admin database principal and was refused by its
assertion: fixed with a separate actual viewer row, not weaker role checks.
Four negative/contract unit groups pass. Full server suite against a fresh TCP
PostgreSQL database passes. An earlier local full-suite invocation used a Unix
socket DSN shape incompatible with three existing migration tests' URL rewrite;
it failed those connection checks. Corrected explicit TCP DSN/fresh DB passes. A repeat incorrectly reused that
non-reentrant test DB and failed duplicate fixture rows; acceptance requires
another fresh database, not a weaker assertion.
Native Docker/real provider result remains pending until exact-tip full logs.
This covers only profile creation and HTTP telemetry, not all writes/background
jobs, physical meter decoding, full production compatibility or secure Upgrade.

Provider v46 70442b4 ACCEPTED all3 exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43275108016
Full raw302030characters/3211lines inspected. Ten explicit write-proof outputs
retain ten synthetic profiles/ten deduplicated events across original89,
candidate93, dedicated previous-image gated compatibility, previous restoration,
settled previous recovery, successful candidate and lost original-HBA-release
fresh recovery through final direct93. Each checks exact saved profile/readback,
HTTP telemetry latest/count/retry, invalid-range rejection and actual viewer,
unauthenticated and other-tenant denial. Original identity/source tenant stays
untouched. Actual provider/Coordinator rollback/commit/release-loss/no-SQL-replay,
original HBA and direct Cmd/PID1/health/image checks pass, then all prior native
components and synthetic-caller transaction paths pass. Provider529s/6250calls;
Linux overall14m24s. Exact temporary branch deleted204/API404/main unchanged,
copied token removed. Scoped profile-create/HTTP-telemetry compatibility only,
not every write/background task, production callback authority, secure owner
Upgrade/menu, physical meter or owner activation acceptance.

Production retained-read client v47 local implementation, native pending:
secure-application-client.py supplies the formerly caller-owned authenticated
transport without activation/menu authority. Exact origin, existing single-link
owned private session file, live /v1/me expected user/tenant/admin/no-key/no-scope
checks before/after complete inventory. Explicit CA/hostname TLS1.2+ trust;
plain HTTP only for explicitly selected numeric loopback old API. Fixed GET-only
paths, no proxies, redirects, cookies or token renewal; response status/type/
length/deadline/size/JSON duplicate fields checked. No response/session text in
transport errors. Snapshot is still limited retained lists, never full runtime
or compatibility acceptance. Eight local actual TLS/HTTP contract groups pass,
including mismatched hostname, identity/scope/key/role denial, mid-inventory role
change, proxy bypass, deadline and private file guards. Local actual old89/new93/
old93/new93 API processes pass live identity/snapshot/compare. Native harnesses
now use this client for all actual application retained inventories and an
activated HTTPS read on exact synthetic hostname/CA, with test-only DNS mapping.
No production login or owner deployment changed; exact native proof pending.

v47 64ea2c4 NOT accepted: Linux failed2m12s, both Windows jobs pass.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43278926457
Full raw31715characters/404lines inspected. New client contract positive HTTPS
and explicit-loopback cases fail before actual native app/provider/HTTPS proof.
Exact committed tests reproduced under Python3.14 (same two errors); disposable
diagnostic copy localizes OSError9 at borrowed sock.settimeout after HTTP/1.0
getresponse closes/detaches that socket object while response file remains live.
Exact branch deleted204/API404/main unchanged/copied token removed.

v48 local correction owns a raw os.dup socket descriptor for deadline shutdown/
timeout only, never TLS reads/writes, and closes it finally. SSLSocket.dup is
unsupported, caught in a first local attempt before publication. Nine regression
groups pass on Python3.10 and3.14, including synthetic borrowed-closed descriptor
and real TLS/HTTP tests. Actual old89/new93/old93/new93 read-client identity/
snapshot/compare also passes. No trust/redirect/identity/size/deadline guard was
relaxed. Corrected full native integration is still pending.

v48 02d2d31 ACCEPTED all3 exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43282304658
Full raw302272characters/3218lines inspected. Actual production retained-read
client passes on activated HTTPS API using exact synthetic hostname/CA, live
unrestricted expected admin before/after inventory and removed private session.
Application native harness now uses this production client throughout actual
provider rollback/previous compatibility/commit/lost-release recovery/final direct
normalization. Ten scoped profile-create/HTTP telemetry proofs retain10 profiles/
10 deduplicated events. Prior operator/gate/group/both transport and synthetic
caller transaction regressions pass. Linux14m5s, no native error. Exact temporary
branch deleted204/API404/main unchanged/copied token removed. v47 failed tip
remains recorded. Client transport is accepted within fixture scope, not full
runtime/background compatibility, owner authority or secure Upgrade/menu entry.

v49 local concrete runtime callback implementation, native pending:
secure-application-acceptance.py checks exact planned running Docker service
images/definitions/health/IDs before and after live retained-client comparison.
Candidate additionally executes only the checked already-running API CID's
read-only tenantctl capabilities/setup-readiness and setupctl inspect-reviewed,
requiring exact reviewed93 object/history and current ledger. It does not run
sidecars or SQL apply. An explicit supplemental behavior verifier remains
mandatory and must raise when unproved; previous-image compatibility stays a
separate Coordinator callback. A retained match is never full runtime authority.
Previous acceptance does not assume old tenantctl supports new operator actions.
Five local guard groups pass on Python3.10 and3.14. Native provider now invokes
the concrete callback around its additional synthetic10-stage write proof, with
unique temporary session files supporting nested reads and checked cleanup.
No owner/menu activation, full background compatibility or hardware proof.


v49 1c9a5b7 NOT accepted: Linux failed6m49s; both Windows pass.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43285950155
Full raw275702characters/3081lines inspected. Concrete runtime callback reaches
deliberate candidate behavior failure, then rollback pre-stop drain rejects a
false pg_terminate_backend result. PostgreSQL log records a PID no longer a
backend. This is a real unverified recovery failure; race investigation remains,
not permission to ignore false results. Exact branch deleted204/API404/main
unchanged/copied token removed. v48 remains last accepted native tip.


v50 local correction, native pending: PostgreSQL snapshot-to-termination race
diagnostic in a disposable local DB captures an actual PID, closes admission,
lets that backend exit during an explicit pause, then reproduces false plus
PID-not-backend warning with fresh closed/zero-session state. First diagnostic
ordered the pause incorrectly and captured no rows; dependency-fixed version
reproduces it. This is a controlled mechanism proof, not unchanged-query replay.
DockerCatalog now rejects malformed drain results and reconciles false only
after a separate fresh exact cluster/database/OID/owner primary/admin check,
no prepared/replication writers, closed admission and zero sessions. It returns
an empty remaining-drain result, not a fabricated successful termination.
Remaining sessions, reopened admission, identity drift and unknown results
stay failures. Provider/fence guards and their own fresh postchecks remain.
Ten local contract groups plus actual disposable SQL/lost-ack/retained-row
proof pass on Py3.10; ten contract groups pass Py3.14. An initial new live fixture
correctly failed because psycopg connection contexts commit but do not close
connections; explicit fixture close fixes it, no guard change. Full fresh TCP
server suite passes. Native deterministic stale-PID diagnostic is added but
not yet run. Full provider rollback/recovery native proof remains pending.


v50 56202f2 ACCEPTED all3 exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43289987194
Full raw304867characters/3238lines inspected, Linux13m55s. Actual deterministic
stale-PID false case reconciled only after fresh exact closed/zero-session
catalog proof. Full real host provider forced-candidate-failure rollback,
dedicated previous-image compatibility, exact restoration, commit, lost original
HBA release and fresh settled recovery without SQL replay all pass. Six concrete
runtime acceptance outputs check old/new actual identities/health/retained client,
with running candidate operator93 checks. Ten scoped profile/HTTP telemetry
proofs retain10profiles/10deduplicated events. Final direct Cmd/PID1/images/health,
original HBA and prior HTTPS/pair/source/operator/gate/group/caller tests pass.
Exact temporary branch deleted204/API404/main unchanged/copied token removed.
v49 failed run remains recorded. v50 is now the last accepted native tip, still
fixture scope, not owner authority/full background compatibility/secure Upgrade
menu, physical meter or owner environment acceptance.

v51 local dedicated previous compatibility callback, native pending:
PreviousCompatibility separately requires the exact previous image's released
compatibility gates for every writer, checked old runtime/health/identities and
retained-client comparison before/after mandatory scoped behavior. Gate IDs
must remain unchanged afterwards. Candidate operator support is never assumed
for old images. Approval flags, absent behavior/group/writers, wrong paths,
held/replaced gates, transport failure and unproved behavior refuse. Seven
contract groups pass Python3.10+3.14. Actual provider's dedicated compatibility
stage now uses the production callback around its existing synthetic write and
real gate checks. This is distinct from post-rollback acceptance and remains
limited to proved behavior; no owner authority/full background compatibility,
connection-probe defaults or secure menu activation is introduced.


v51 dfec90f NOT accepted: Linux cancelled15m16, both Windows pass.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43293946145
Full raw299446characters/3206lines inspected. Dedicated production previous-group
compatibility output, six runtime acceptances, ten writes/full provider lifecycle
and static operator pass, but cancellation precedes remaining gate/group/caller
regressions. Native Linux timeout15min is consistent with observed cancellation;
no product assertion failure inferred. Exact branch deleted204/API404/main
unchanged/copied token removed/browser released. v50 remains accepted.
Native test-budget headroom needs correction without weakening assertions or
requesting broader account/token permission.


v52 455edbf ACCEPTED all3 exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43297610917
Full raw312457characters/3295lines inspected; Linux20m1s. Native Linux budget
only changed15->25min through existing authenticated UI on temporary branch,
no new token/account permission, commands/assertions unchanged. Exact one-line
workflow diff/parent8f9e1ef and all180 changed remote blobs verified. Dedicated
production PreviousCompatibility callback passes the real old-image released
group/retained client/scoped behavior stage; six runtime acceptances and ten
write proofs retain10profiles/10deduplicated events. Full provider rollback,
previous compatibility, commit, lost original HBA release/fresh noSQLreplay and
final direct runtime pass. All formerly-cut-off static operator/gate/group/caller
regressions complete. v51 cancelled run remains recorded. Exact temp branch
deleted204/API404/main unchanged/copied token removed/browser released. v52 is
last accepted native tip, still not full background compatibility, concrete
owner authority/connection probes, secure Upgrade menu or physical meter proof.

v53 local bounded static connection-probe component, native pending:
Candidate setupctl connection-readiness takes exact version/transport/private
or loopback IPv4/port/database/user/password only over bounded stdin. Duplicate,
unknown, extra, ambient PG settings, public/wildcard/DNS endpoints and implicit
TLS downgrade refuse. Explicit private_tcp_plain only; TLS/socket support is
not inferred. No environment credential/passfile/DNS/fallback, application SQL
write or upstream error body is used. A seven-second bounded read-only session
returns connected exact database/user/server/client or narrowly classified
HBA denial/database closed/authentication failure; all other failures unverified.
It is evidence, not owner authority or a provider connection callback.
Actual disposable local PostgreSQL connected/closed/no-schema-change tests and
negative parser/classifier contracts pass. First local check found PostgreSQL
inet::text includes /32; host(inet) now returns canonical endpoint strings.
Native provider acceptance additionally invokes the exact running candidate
API CID's new binary for exact peer endpoint, wrong-password SCRAM refusal and
unlisted-user HBA refusal while restricted. Original restored HBA is not assumed
to reject that user by HBA. No probe sidecar/image pull/old-image tool assumption,
provider-default activation or new credential is introduced. Production host
namespace staging/probes and owner-review/menu remain separate work.


v53 7ea2bd1 ACCEPTED all3 exact-tip native jobs:
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43305216074
Full raw305957characters/3246lines inspected, Linux13m35s. Four real candidate
connection-readiness proofs confirm exact PostgreSQL server/client endpoint and
wrong-password SCRAM refusal. Two restricted-admission stages additionally
confirm unlisted-user HBA denial; original restored HBA is not claimed to deny.
Dedicated previous compatibility, six runtime acceptances and ten retained-write
proofs plus full provider rollback/commit/lost-HBA fresh noSQLreplay/direct-final
and remaining operator/gate/group/caller regressions pass. Exact temp branch
deleted204/API404/main unchanged/copied token removed/browser released. v53 is
last accepted native tip within fixture scope. Production namespace staging and
provider connection callback, owner authority/menu, full background compatibility
and physical meter acceptance remain separate unfinished work.

v54 local exact static host-probe staging, native pending:
secure-postgres-hosttool.py stages only /bin/setupctl from an exact never-started
network-none immutable candidate image source. Private journal binds source,
image, file path/hash and stage before effects. One bounded regular Linux amd64
ELF only; no links/duplicates/foreign archive paths. Host binary is owned,
single-link, mode0500 and hash-checked; changed bytes/mode/symlink/identity refuse.
Fresh explicit resume handles actual source create/copy/remove lost acknowledgements
without adopting an unrelated source or overwriting changed host evidence.
Explicit checked release removes only the verified staged binary. No passwords,
namespace choice, connection, image pull/start, provider-default activation or
owner authority are introduced. Seven local test groups pass Python3.10+3.14. Dynamic/interpreted ELF binaries
and replacement source container IDs refuse. A partially written host file fails
closed and remains for explicit investigation, not automatic overwrite.
Native harness adds actual candidate extraction, create/remove acknowledgement
loss, fresh resume, host ELF execution refusing absent endpoint, and checked
cleanup before unchanged provider lifecycle regressions. Native proof pending.
Exact host PID/start/network namespace binding, peer and outside-negative proofs,
production callback/owner review/menu remain separate unfinished work.


v54 native NOT ACCEPTED: pre-result fixture review found the new staging hook
passed the image tag despite the production HostTool requiring the exact resolved
sha256 image ID. That ID was already resolved immediately above the hook.
Correction passes that immutable ID, not a weakened guard. Corrected native
validation remains pending. Local-only namespace prototype is separate and is
not included in this staging correction. No owner changes or activation.
v54 actual all3 final: Windows both PASS; Linux FAIL5m28s, full raw260099chars
2944lines inspected. Exact strict-image constructor refusal confirms diagnosed
tag-vs-ID fixture argument. No staging/provider acceptance inferred.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43310443042
https://github.com/abhisekrath00-ux/iot-platform/commit/17e7dbcb087d0f3d13f15e3ff7bc115fd8e803db/checks/131504824661/logs
Corrected v55 staging-only tip reuses the existing temporary validation branch;
prior failed run remains keyed by exact SHA. Seven local groups still pass both
Python versions. Full Go suite on reused disposable v54 DB failed on retained
fixture primary keys; separately created fresh v55 DB full suite passes. No
product code/assertion was changed for that fixture contamination.

v55 ba713ce ACCEPTED exact all3 native jobs; Linux12m32s, full raw302188chars
3218lines inspected. Actual static host probe staging confirms exact immutable
candidate image, never-started network-none source, private hash-checked amd64
ELF, actual create/remove lost-ack fresh recovery, host execution rejecting an
absent endpoint, and checked binary/source cleanup. No namespace/provider
connection callback proof is inferred from this staging-only acceptance.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43311923258
https://github.com/abhisekrath00-ux/iot-platform/commit/ba713ce7d9b9dcacaca41f8512dee5c528094306/checks/131509904811/logs
Dedicated previous compatibility, 6 runtime acceptances, 4 bounded candidate
connection proofs, 10 scoped retained write proofs, full provider rollback/commit/
lost original-HBA acknowledgement fresh no-SQL-replay/direct final runtime, and
all remaining operator/gate/group/caller regressions pass. Expected fixture JWT
rejection output is not a job failure. Exact temporary branch deleted204/ref404;
main unchanged, copied token removed. v54 failed attempt remains preserved.
v55 now last accepted native tip. Separate untracked namespace prototype remains
local-only, not part of accepted tip. Actual peer namespaces/outside-negative
production probes, owner review, secure Upgrade/menu, full background compatibility
and physical meter remain unfinished. No owner PC changes or update-ready claim.

v56 local pinned host network namespace component, native pending:
Exact accepted application CID/image/start/project/service/network ID/source IP,
Docker host PID, kernel start ticks/boot ID and full-CID cgroup paths are checked
before and after pinning a pidfd and network namespace FD. Host/shared-container
network or PID modes, paused/restarting/exited processes and identity drift refuse.
Only the network namespace changes: candidate static host binary is pinned by FD
and SHA, executed through nsenter with a clean environment and credentials on
bounded stdin only. Connected results must match exact database/user/source/server;
negative outcomes remain narrow/unverified, never assumed success. No container
start/exec, old-image probe binary, sidecar or provider-default activation.
Nine local test groups pass Python3.10+3.14 including actual descriptor lifecycle
with mocked identity, actual foreign-process refusal, pre/post drift, changed host
binary and malformed result contracts. Real Docker namespace behavior is pending.
Native harness now runs after old API/ingest/MCP are running, with already resolved
immutable candidate image ID; all3 actual pinned source/server read-only connections
and wrong-password SCRAM refusals plus checked cleanup are mandatory. Prior full
provider/regressions unchanged. No outside-negative, production connection callback,
owner authority, menu, full background compatibility or hardware proof inferred.

v56 199781a ACCEPTED exact all3 native jobs; Linux13m42s/fullraw305732chars
3241lines inspected. All3 previous-image API/ingest/MCP real process start ticks,
PID/cgroup CID/boot and pinned pidfd/netnsFD checks passed. Candidate static host
binary ran byFD in network-only nsenter with clean env/stdin-only synthetic
credentials; exact distinct source/server endpoints and wrong-password SCRAM
refusal passed for all3, with checked staging cleanup. No sidecar/previous-image
probe binary assumed. Actual cgroup/namespace behavior now proved in this fixture.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43319670583
https://github.com/abhisekrath00-ux/iot-platform/commit/199781a3b44c2db8ea041f2e714c03fdfe6e5c66/checks/131536725149/logs
Prior static staging/dedicated previous compatibility/6 runtime acceptances/
4 candidate endpoint proofs/10 scoped retained writes/fullprovider rollback+
commit+lost-HBA fresh noSQLreplay/directfinal and all operator/gate/group/caller
regressions PASS. Exact temporary branch deleted204/ref404/main unchanged,
copied token removed. v56 now last accepted native tip. Still no production
outside-negative endpoint/callback, owner review/secure Upgrade menu, full
background compatibility or physical meter acceptance. Owner PC unchanged.


v57 explicit existing outside source, local tests pass/native pending:
Separate web-only OutsideNamespace preserves API/ingest/MCP writer binding.
Exact pre-existing reviewed web CID/image/start/network/IP plus pinned kernel
process identity are required; no host-as-outsider assumption, probe sidecar,
old-image tool, endpoint discovery or automatic provider activation. OutsideProof
requires original first-match SCRAM admission and exact rendered restricted HBA
bytes excluding that source, fresh live server bytes and raising scope checks
before/after the bounded host probe. Only hba_denied proves outside exclusion;
authentication_failed, database_closed, timeout/unverified and connected fail.
An HBA reject precedes database admission checks, so this negative cannot prove
closed datallowconn. That requires separate catalog/admitted-source evidence.
Fifteen local groups pass Python3.10+3.14; native real existing web connection ->
restricted denial -> admitted API-peer handoff still denying web -> lost reload
acknowledgement/fresh journal+namespace recovery -> original connection restore
is wired, not yet accepted. Existing full provider/regressions remain mandatory.
Production callback lifecycle, owner review/secure Upgrade menu, full background
compatibility and physical meter remain unfinished. No owner PC changes.
Local full Go run initially used localhost and correctly failed strict IPv4
connection request parsing in setupconnection. Separate fresh disposable DB with
127.0.0.1 passed the complete suite; no parser or assertion weakened.

v57 88104fa ACCEPTED exact all3 native jobs; Linux20m35s/fullraw320849chars
3325lines inspected. Actual already-running web source172.18.0.3 to server
172.18.0.4 returned original connected, seven exact restricted hba_denied proofs
across local-only barrier/API peer handoff/lost reload acknowledgement/fresh
journal+host tool+namespace recovery, then original connected again. All nine
results bind the same exact web CID and HBA byte digest. Exact original HBA
bytes/mode/owner and staging cleanup pass; no new probe sidecar or old-image tool.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43331192485
https://github.com/abhisekrath00-ux/iot-platform/commit/88104fab39a2467c84df9c2319d512e831d5e37e/checks/131576036392/logs
Prior all3 real old-writer namespaces/static staging/dedicated previous
compatibility/6 runtime acceptances/4 candidate connection proofs/10 retained
scoped writes/fullprovider rollback+commit+lost-HBA fresh noSQLreplay/directfinal
and all operator/gate/group/caller regressions PASS. Exact temporary branch
deleted204/ref404/main unchanged/copied token removed. v57 last accepted native
tip. This is outside endpoint component evidence, not production callback/menu
activation. HBA refusal does not prove database_closed. Production callback
transition lifecycle, owner review/secure Upgrade menu/full background
compatibility/physical meter still unfinished. Owner PC unchanged.

v58 local closed-phase transition callback, native pending:
Provider now calls its mandatory connection verifier after exact identity-bound
catalog close/drain/zero sessions and before reopening. Failures leave closing
and datallowconn=false for fresh recovery. A separate connection_guard validates
scope/marker/receipt, immutable host/database and exact current/pending HBA bytes
without recursively calling normal provider.guard/check/reload while closing.
LocalAdmission requires an independent exact local database_closed result, never
an HBA/authentication/timeout failure. Candidate setupctl connection-readiness now
accepts explicit canonical local_socket plus exact port/db/user/empty password,
with clean environment, no TLS/network fallback and bounded read-only query.
Checked LocalOperator.connection runs the exact hashed staged candidate binary
with JSON stdin and env-i, not SQL text or credentials in its arguments.
psql verbose connection errors did not expose SQLSTATE55000 in a disposable local
experiment, so that error-text approach was discarded rather than accepted.
Actual Go socket connected/closed tests pass; 10 provider/7 operator/4 admission
groups pass Python3.10+3.14. Native operator connected -> closed55000 -> reopened
and real provider per-closed-transition local static proofs are mandatory next.
Native network callbacks remain fixture-only, not the full production connection
assembler. No owner review/secure Upgrade menu/full background/hardware proof.

v58 8d18697 ACCEPTED exact all3 native jobs; Linux19m7s/fullraw319497chars
3317lines inspected. Thirteen actual provider closing transitions independently
verified exact closed catalog/zero sessions plus candidate local static probe
SQLSTATE55000 before reopening. Nonrecursive production transition guard binds
marker/receipt/current-pending HBA/database/host while intentional closing holds.
Actual operator fixture connected -> closed55000 -> reopened connected with the
same checked candidate executable and no psql text/auth/HBA inference passed.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43344952484
https://github.com/abhisekrath00-ux/iot-platform/commit/8d186977bb732527c4fe7c4f9290083882e94d3b/checks/131621954153/logs
Prior all3 old-writer namespace/existing-web outside original+7denials+restore/
static staging/dedicated previous compatibility/6 runtime acceptances/4 candidate
endpoint proofs/10 scoped retained writes/fullprovider rollback+commit+lost-HBA
fresh noSQLreplay/directfinal and all operator/gate/group/caller regressions PASS.
Exact temporary branch deleted204/ref404/main unchanged/copied token removed.
v58 last accepted native tip. Local closed-phase callback now has actual evidence;
full network callback assembly remains fixture-only. No production network
assembler/owner review/secure Upgrade menu/full background/hardware acceptance.
Owner PC unchanged.

v59 explicit connection callback assembly local-only/native pending:
Connections composes transition-bound local admission, existing reviewed web-only
outside proof and all3 dynamic exact durable admitted writer network namespaces.
Exact PG CID/image/start/bridge/address/port plus reviewed writer images and full
service/CID/HBA tuple inventory are checked before and after probes. Wrong port,
network/image/peer tuple, auth/unverified refusal, post-proof source drift and
recursive callback fail closed. Constructor does not stage tools or grant scope.
Six local groups pass Python3.10+3.14. Native provider fixture now uses this
callback in every original/restricted/exclusive reload and close transition,
not synthetic psql peer-sidecars or a new outsider sleep container. Staged host
and local candidate tools persist across failed/recovered calls and cleanup is
explicitly after final provider original admission/direct-runtime settlement.
Actual runtime cost and full native rollback/compatibility/commit/lost-release
recovery remain unverified. No menu/owner/full background/hardware acceptance.

v59 a9e768b NOT ACCEPTED: LinuxFAIL12m34s, bothWindowsPASS. Fullraw300214chars
3265lines inspected. Production callback proved restricted/no-peers, exclusive
local55000 and four all3 pinned writer handoffs, then refused rollback empty HBA
peers because durable provider still listed previous3peers. close_local persisted
empty closing intent AFTER handoff, incompatible with exact callback during
reload. Correct by persisting closing/empty peers BEFORE handoff, matching admit's
intent-before-effect ordering. Exact tuple guard remains unchanged; new local
regression covers normal and lost handoff acknowledgement. Native cost448s to
first candidate acceptance, full budget remains unverified. v58 stays accepted.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43349002028
https://github.com/abhisekrath00-ux/iot-platform/commit/a9e768b5ada2a1975fe8469a6dbc761c381ea56f/checks/131635212283/logs

v60 2bd05c7 NOT ACCEPTED: Linux cancelled25m16s due explicit maximum25m
execution limit, bothWindowsPASS. Fullraw317107chars3331lines inspected.
35 full network callbacks:18 restricted0,9 exclusive0/local55000,8 restricted3
all pinned writers; dedicated actual previous-image compatibility passed with
retained-client and synthetic profile/HTTP telemetry writes3profiles3events.
No completed rollback restoration/commit/fresh release, no inferred acceptance.
Ordering regression no longer fails before this point. Laststage1165s9265commands.
Isolated next test-budget correction25->60min, no commands/assertions/runner
change or cross-boundary caching. Live repoAPI private:false/public and current
jobs standard GitHub Actions ubuntu-24.04/windows-2025 match GitHub free/unlimited
public-repo standard runner policy verified before edit/launch. No paid run.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43352898577
https://github.com/abhisekrath00-ux/iot-platform/commit/2bd05c7e3ca2a8e5910e40800f76225289552679/checks/131647967864/logs
https://docs.github.com/en/actions/reference/runners/github-hosted-runners
https://docs.github.com/en/actions/concepts/billing-and-usage

v61 4a0e19d ACCEPTED scoped native fixture: all3jobs pass/Linux39m34s. Fullraw
388406chars3762lines inspected including subsequent operator/gate/group/caller
regressions, no error annotation.68production Connections callbacks completed:
28restricted/no-peers,16all3pinned-writer sets,13exclusive local55000,11original.
Every phase uses existingweb outside and exact namespace-FD static host probe,
no new probe sidecar. Forced candidate failure -> dedicated previous compatibility
-> exact restoration; committed/lost original-HBA ack -> fresh noSQLreplay ->
direct original Cmd/PID1/images/health complete.6runtime acceptances/10writes
retain10profiles10deduplicated events. Host/local staging explicitly cleaned only
after each settled provider release. v59 tuple-ordering failure and v60 timeout
remain recorded above. Fresh guards/assertions unchanged, Linux testbudget60min;
publicrepo standardrunner free verified before launch. No owner/menu activation,
full background compatibility or physical meter proof. Those remain gated.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43356585740
https://github.com/abhisekrath00-ux/iot-platform/commit/4a0e19d3ed6b2e68208212c42de49c46a7fb7401/checks/131659947147/logs

Next unaccepted production lifecycle extraction: secure-application-production.py
wraps the concrete Provider with explicit reviewed outside endpoint/request,
mandatory fresh reviewer, plan-manifest-bound candidate image and durable original
HBA. No CLI/default discovery/menu/owner authority. Native fixture now subclasses
this production entry, not a fixture-owned staging/callback/cleanup closure.
Host/local artifacts resume exact partial generations, remain on proof failure,
and clean only after base provider settlement. Released host generation after a
lost cleanup acknowledgement can roll over ONLY on explicit settled original
recovery, preserving its verified terminal record; no activation/restricted or
unsettled restaging. New actual native lost-cleanup recovery/noSQLreplay assertion
pending;9local assembly groups pass both Python versions. Not yet accepted.

v62 060b69c NOT ACCEPTED: both Windows jobs pass, Linux fails27m49s with
Python interpreter segmentation fault/exit139, not the60minute job budget.
Fullraw338245chars3454lines inspected.45 actual production-lifecycle connection
proofs complete:19restricted/no-peers,12all3pinned-writers,9exclusive55000,
5original. Forced candidate failure -> previous compatibility -> exact restoration
and actual host/local cleanup complete. Fresh recovery after both cleanups begins
and restages the exact host source, but terminates before its acceptance. No
noSQLreplay/finalcommit/downstream acceptance inferred. Last periodic120second
C faulthandler diagnostic is in json decoding; root cause unresolved, not claimed
as a product assertion failure or a timer correction. v61 remains last accepted.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43370680997
https://github.com/abhisekrath00-ux/iot-platform/commit/060b69c62406e07f2be2bd0211d57fd5a6d06f80/checks/131705454454/logs

v63 DIAGNOSTIC RERUN PENDING: production logic/guards/assertions unchanged.
Enable fatal-signal tracebacks, replace periodic C-stack dumping with a Python
watchdog stack sample, and trace bounded command identity/completion in the new
cleanup-recovery segment. These are diagnostics, not a proven correction.
Added real HostTool/LocalOperator durable-record contract through three complete
prepare/prove/cleanup/archive/restage generations with no apply-reviewed call;
only the Docker transport is simulated locally.10production/11provider/6Connections
groups pass bothPython, fullfreshGo passes. Initial local Go invocation used
localhost and was correctly refused by the IPv4-only connection contract; fresh
127.0.0.1 rerun passes. Native proof still required.

Enterprise readiness remains a full-outcome gate, not this fixture's result:
secure access, install/update reliability, backup/restore, service-failure recovery,
full server-to-edge behavior and deployment/hardware limits. User Nov13 requires
architecture/caching review including invalidation, DB hot paths, background work,
scaling/monitoring and integrated load/failure evidence. Ground changes in existing
paths; no distributed topology or paid infrastructure presumed. Free/local only.
Fresh edge EXE must be downloadable/version-verified from the dashboard. Once-ready
handoff must include server update steps and that fresh edge path together.
Existing owner installation remains unchanged; no main merge/tag/public release.

v63 8f0354c ACCEPTED scoped native fixture, not enterprise/owner readiness:
all3 jobs PASS, Linux44m26s. Fullraw821077chars6371lines inspected including
all downstream operator/startgate/group/namespace/caller regressions.73production
lifecycle callbacks:28restricted/no-peers,16all3pinned-writers,13exclusive55000,
16original. Actual loss after both host/local cleanup -> fresh settled recovery
archives released generation, restages/reproves exact tools, no schema replay or
early admission release. Committed/lost original-HBA acknowledgement -> fresh
noSQLreplay -> direct final Cmd/PID1/images/health also complete.8runtime acceptance
checks/12writes retain12profiles12deduplicated events. Exact input/image/manifest,
transition guards and outside existing-web/no-sidecar assertions preserved.
The v62 interpreter crash remains unexplained, not retroactively accepted or
claimed fixed by the diagnostic adjustment. Subsequent cache changes are NOT
native-tested by this exact v63 run; their separate Go/local evidence is in
enterprise-readiness.md. Production reviewed entry/menu, full background behavior,
combined server/dashboard-EXE handoff and physical meter remain open.
https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43378525884
https://github.com/abhisekrath00-ux/iot-platform/commit/8f0354c13aba23f69eb860d700771118af63cea2/checks/131730606818/logs

### Exact60f diagnostic rerun, 13 November 2026, 18:02 IST

https://github.com/abhisekrath00-ux/iot-platform/actions/runs/43415964761
completed all3 jobs successfully at exact60f97e794aeba60c8433c61bf221de815350b509.
Linux took46m30s. The provider assertions include lost host/local cleanup fresh
recovery, forced previous-runtime restoration, committed original-HBA release
acknowledgement loss and fresh final recovery without schema replay. Windows
contracts and real service lifecycle pass within their existing limited scopes.
General CI43415919898 also passes all4jobs at this exact tip.

The c743 exclusive-guard failure remains unexplained.60f adds fixed diagnostic
facet codes, not a correction to admission semantics. No failure occurred in
this rerun, so it did not identify the old failed facet. A separate disposable
local PostgreSQL14.24 fixture observed transient autovacuum workers in a closed
database despite zero successful client connects. This is a candidate mechanism
for a zero-session refusal, not proof of the CI failure's cause or justification
to exclude workers. Every exclusive predicate stays unchanged.

The819173-byte/6348-line Linux raw log is retained. Targeted assertions and
transition sections were inspected, not every Docker trace line. This is scoped
synthetic fixture evidence, not owner installation or full compatibility,
background-effect, hardware, final EXE or executable secure-upgrade acceptance.

### Derived-background gate remains open

The real compiled-ingest characterization
`TestIntegrationCompiledIngestDerivedWorkGap` reproduces telemetry commit ->
synthetic flow-run database failure -> restart -> exact duplicate skipped,
leaving the derived run absent. A subsequent unique ordered-barrier event has
a run. This is a known missing-work window, not durable recovery acceptance.
See `enterprise-readiness.md`, "Open ingest-derived durability gap" for fault,
scope and replay-key details. The existing mTLS pipeline test invokes this
separate plaintext-loopback fixture; it supplies no enabled delivery channel
or actuator. A passing characterization does not satisfy full background or
previous-image compatibility and must not enable the production update entry.
No automatic retry of external effects is added. Owner environment unchanged.

A follow-up flow guard refuses asynchronous notification scheduling when the
telemetry-driven `flow_runs` INSERT fails. Real-PG fault injection first observed
a recording stub called with no run row; the guard and healthy recorded control
are regression-tested. This is not durable derived recovery: graph-side effects
before run recording and lost delayed dispatch after recording remain outside
it. No schema change, external retry or production update availability follows.

Position-source configuration recovery now returns real database read errors
without negative-caching them and scopes cached entries by tenant+device.
Real-PG cancellation/lock-deadline recovery and tenant-cache isolation tests
fail on prior code and pass with immediate healthy reload. This adds no schema,
replay or durable derived-work guarantee. Previous-image/background acceptance
and production Update entry remain gated; owner setup is unchanged.
