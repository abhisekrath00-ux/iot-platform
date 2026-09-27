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
