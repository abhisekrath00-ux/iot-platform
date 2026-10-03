# Gated control nodes for flows (design, not built)

Status: partly built (2026-10-03), tested locally, never against real hardware or a broker.
Built: the control target registry (migration 0036, `/v1/control-targets`, Control page list) and the **`control` flow node**
(approval path only). Not built: automatic execution for `automatic` targets, a `control.result` trigger, an asset-level
or per-flow-version audit join, and Node-RED import or export of this node (an imported `control` node is refused).
Prerequisite 1 (edge Modbus write executor with allowlist) already existed before this document was written.

What the node does today: a flow run asks for a change to one target. The server checks that the tenant has the
`control_nodes` feature on, the target exists and is switched on, the value is inside the target's bounds (never clamped),
the target's rate limit (default 6 per hour) and the flow's cap of 3 requests waiting for approval. If all pass it inserts a
`pending_approval` command, requested by a per-tenant service user with the viewer role (so it can never approve and the
existing four-eyes rule holds), with the flow name and triggering reading as the reason, and writes an audit row
`control.request`. The node never sees the approval; output 1 means "request raised", output 2 means "refused". Dry runs
and the simulator get no requester, so they raise nothing. Saving, importing or publishing a flow with this node needs an admin
and the tenant feature. A run raises at most one request. The approver sees what is asked and why on the Control page.

**For a target set to `automatic`, the node still raises a normal approval request** (and says so in the run log), because
the automatic executor is not written. That is the stricter behaviour, chosen on purpose until it is built and reviewed.

### Owner decision, 2026-10-03

The owner chose that the approval requirement is a setting, not a hardcoded rule: a siren or buzzer (an alarm output) may be
set to fire **automatically**, for example when the server link is down or an alarm rule hits, while every other control action
needs a second person's approval. The default is approval. The setting is made by an admin per target, is audited, and
**automatic is accepted only for `alarm_output` targets** (on/off only); the database refuses it for Modbus writes.
This answers the open question below. The registry stores the choice; the code that would act on `automatic` is not written (see above).

## Goal

Let a flow end in a physical action (MQTT publish, Modbus register write) without giving flows a way around the
existing control safety model: request, approval by a different human, short TTL, audit, measured outcome.

## Starting point (verified in this repo)

- `POST /v1/commands` creates a `pending_approval` row. `approveCommand` requires an interactive user session with
  admin/operator role and rejects self-approval (`requested_by <> approved_by`). API keys can request, never approve.
- Per `docs/hazard-analysis.md` the actuator executor is not wired yet. No command reaches hardware today.
- Flows currently have no side effects beyond notifications, webhooks and the gated HTTP node.

## Rule 1: a flow node never actuates. It only requests.

A `control.request` node calls the same code path as `POST /v1/commands`, as principal `flow:<flow_id>`.
The command lands as `pending_approval`. Approval is a human action in the Control page, as today.
Consequences:
- Four-eyes holds with no new rule: the requester is the flow, the approver is a person. A flow principal can
  never approve (same check that blocks API keys).
- Node outputs: 1 = request created, 2 = refused (policy, rate limit, feature off, dry run). The node cannot
  observe approval, because approval can happen minutes later. A separate `control.result` trigger is a later option.

## Rule 2: only allowlisted targets

The node holds a target id, not a topic, host, address or value expression.
- An admin defines control targets in a registry (tenant scoped): kind (`mqtt_publish` or `modbus_write`),
  device, fixed topic or register, value type, min/max, allowed fixed values, max rate, TTL, risk class.
- A flow chooses a target and a value from that target's allowed set or range. Free-form values from telemetry are
  clamped and rejected outside the range, never passed through.
- Register writes are limited to registers marked writable in the device profile. Config and reset registers stay
  unwritable (existing rule for Selec meters).

## Rule 3: execution stays at the edge, behind existing gating

After approval the command goes to the edge agent over the existing signed command channel with TTL.
The edge validates against its own local copy of the allowlist and refuses anything outside it. The cloud
cannot widen the edge allowlist remotely. Class `alarm` outputs (sirens) keep their separate local-rule path.

## Rule 4: limits and kill switch

- Off by default per tenant (`control_nodes` feature, admin only), like `http_nodes`.
- Only an admin can save or import a flow containing these nodes.
- Per-target rate limit and a per-flow pending cap, so a noisy rule cannot flood approvers with requests.
- Global and per-target disable switch; disabled targets refuse at request time and at the edge.
- Dry runs (editor Test button) never create a command.
- Every request and refusal is audited with flow id, flow version, trigger reading id and value.

## Out of scope

Auto-approval, approval by role without a second person, direct cloud-to-device writes that bypass the edge,
arbitrary topics or addresses, Node-RED import mapping for these nodes (imports of such nodes are rejected).

## Prerequisites (order of work)

1. Wire the edge actuator executor (today a TODO), with its local allowlist and a simulator-backed test.
2. Control target registry: migration, admin API, UI page.
3. `control.request` node, feature flag, rate limits, tests including: flow cannot approve, out-of-range value
   refused, disabled target refused, dry run creates nothing, edge refuses a command not in its allowlist.
4. Only then Modbus write and MQTT publish executors. Real hardware is untested; label accordingly.

## Open decisions for the owner

- ~~Is a human approver always required, even for low-risk class `alarm` targets?~~ Answered 2026-10-03: configurable per target, default approval, automatic only for alarm outputs.
- Should the approval screen show the triggering reading and flow, so approvers see why? (Proposed: yes.)
