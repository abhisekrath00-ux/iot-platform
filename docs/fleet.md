# Fleet config and update rollout

Staged, reversible rollouts to gateway fleets. The platform-side campaign
state machine is implemented; the edge delivery slice (broker fanout and
on-gateway apply) is scoped below.

## Model

- **Release**: a version plus the SHA-256 of its artifact. In air-gapped
  sites the artifact ships inside the offline bundle - never pulled at
  rollout time.
- **Campaign**: a release, a candidate serial list, and ascending stage
  percents (e.g. `[10, 50, 100]`). Gateways are bucketed into stages by a
  deterministic FNV hash of `campaign_id/serial`, so a gateway never jumps
  cohorts mid-campaign and replays are stable.
- **Assignment**: one row per (campaign, gateway) in the active stage:
  pending -> sent -> acked | failed; rolled_back after a rollback.

## Rules enforced by the API

- Stages strictly ascending, last exactly 100; 1-10 stages.
- A campaign starts at stage 0 and advances only when the current stage has
  no gateways still in flight.
- When failures reach the campaign threshold (default 3) the campaign
  halts: advancing is refused until an operator rolls back or aborts.
- Rollback creates a new single-stage campaign moving every affected
  gateway onto a chosen good release, and marks the old assignments
  rolled_back. Every transition is audited.

## Edge delivery slice (next)

1. On stage entry the platform publishes each assignment as a retained
   message on `t/<tenant>/g/<serial>/fleet` (payload: release, artifact
   sha256, action apply|rollback, campaign id).
2. The edge agent verifies the artifact digest against the bundle, applies
   the config/update, and ACKs on its fleet topic; an API bridge records
   the ACK via `/v1/fleet/ack`.
3. Update artifacts are signed; the edge verifies signature + digest before
   applying, and keeps the previous release for automatic rollback on
   failed boot (see Lifecycle in docs/security.md).

## Endpoints

`GET/POST /v1/fleet/releases`, `GET/POST /v1/fleet/campaigns`,
`POST /v1/fleet/campaigns/{id}/start|advance|pause|abort|rollback`,
`POST /v1/fleet/ack` (operator or edge bridge).


## Edge delivery slice (shipped)

- **Fanout**: `enterStage` (start/advance) publishes each cohort gateway's
  release manifest as a RETAINED message on `t/<tenant>/g/<gw>/fleet`, so
  offline gateways pick up assignments on reconnect. Assignments move
  `pending -> sent` on successful publish; failures stay pending.
- **Edge verify**: the edge agent subscribes its fleet topic, validates the
  manifest addressee, and verifies the artifact against the local digest
  store (`artifact_dir`, default /var/lib/hexmon-edge/artifacts; air-gapped
  bundles pre-stage artifacts by sha256). Config-only releases (no artifact)
  ACK immediately. Digest strings are regex-validated before any filesystem
  access, so a hostile manifest cannot traverse the store path.
- **Auto-ACK**: the edge answers on `t/<tenant>/g/<gw>/fleet/ack`; ingest
  routes it into the assignment row with topic-identity enforcement (a
  gateway can only ack its own assignment). A `failed` ack auto-pauses the
  campaign once failures reach its threshold.
- **Apply**: verified config artifacts install with backup + validation +
  automatic rollback (`fleetctl.ApplyConfig`): the previous config is backed
  up, the new one is atomic-written and re-validated from disk, and the poll
  supervisor reloads in-process (no agent restart, no dropped MQTT
  connection). Identity is pinned - a config naming another gateway or
  tenant is refused. Health-check failure restores the backup byte-for-byte
  and the ACK reports the rollback. Binary self-updates require signed
  artifacts (lifecycle gate in docs/security.md) before they ship.

## Signed manifests (built, simulator/unit-tested only)

- The control plane signs every release manifest with Ed25519 (`FLEET_SIGNING_KEY`, a base64 32-byte seed from the environment, never the database). The signature covers tenant, campaign, release, version, artifact SHA-256 and gateway serial, so a manifest cannot be edited or replayed to another gateway or tenant. Generate a pair with `go run ./cmd/fleetkey` in `server/`.
- An edge with `fleet_public_key` set in its config refuses unsigned, tampered, wrong-key and other-tenant manifests (the ACK says `failed: refused: ...`) before it touches any artifact. Without the key the edge behaves as before (digest check only), and without the signing key the server sends unsigned manifests. Run with both set in production.
- The artifact is bound to the signature through its digest, so a signed manifest plus the existing digest check means a swapped artifact is refused.
- Tests: signature vector shared by server and edge (the two copies of the canonical form cannot drift), tamper cases for each field, wrong key, unsigned, wrong tenant, end-to-end `HandleManifest`.
- Not built and not testable without devices: firmware flashing for MCU or PLC end devices (the LwM2M firmware-update object, vendor bootloaders), signed-artifact key rotation, key revocation lists, a hardware root of trust. This protects edge agent config and release delivery only. Never run against a real broker or a real fleet.

## Dashboard (Fleet updates page)
Admin and operator users get a Fleet updates page: edge boxes with site and status (select by site), releases (add version with optional artifact SHA-256), a staged rollout form (stages like 10,50,100, halt threshold), and a rollouts table with progress and the actions the current state allows (start, advance, pause, abort, rollback). The page checks stages the same way as the server and only shows buttons that make sense; the server still enforces every rule. Unit-tested helpers (3 tests); screenshot-checked against a real API.

Not built in the UI: remote actions on a box (restart, collect logs), per-box agent version and last-seen, a rollback target picker (it uses the first other release), groups beyond site selection. No rollout has run on a real edge box.
