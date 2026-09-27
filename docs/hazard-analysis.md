# Hazard analysis - physical control path

Status: living document. Reviewed at every actuator-driver merge and quarterly
with the threat model. Method: HAZOP-lite over the command path plus an FMEA
table per actuator class. Severity (S) and likelihood (L) are 1-5; risk = SxL
before and after mitigations. Anything residual >= 8 blocks the release gate
in docs/security.md.

## 1. The command path as built (evidence base)

1. `POST /v1/commands` creates a request row (`pending_approval`). Any
   authenticated tenant user may request; requesting never actuates.
2. `POST /v1/commands/{id}/approve` requires admin/operator, rejects
   self-approval (four-eyes: `requested_by <> approved_by`), stamps a
   **5-minute expiry**. The broker publish behind it is deliberately
   **not wired** (`TODO` in approveCommand) - no actuator executor exists.
3. The edge agent subscribes `t/<tenant>/g/<gw>/cmd` and applies two
   independent checks before any executor would run: the action must be in
   the gateway's **config allowlist** (`allowed_commands`), and the command
   must be **unexpired**. The executor itself is unimplemented; accepted
   commands are logged and dropped.
4. The commissioning port test (`docs/commissioning.md`) is read-only by
   construction: Modbus functions 1-4 only, validated server-side and
   re-clamped on the edge. It is not an actuation path.
5. Every step writes audit rows (command.request / command.approve).

Net state: **no software path can actuate physical equipment today.** This
analysis governs what must be true before the first executor merges.

## 2. Hazard log

| # | Hazard | Cause scenario | S | L | Risk | Mitigations (control ref) | Residual |
|---|--------|----------------|---|---|------|---------------------------|----------|
| H1 | Unauthorized actuation | Stolen user JWT used to request+approve | 4 | 3 | 12 | RBAC + four-eyes (two identities needed), short JWT, audit trail, OIDC SSO | 4 |
| H2 | Self-approved actuation | Operator requests and approves own command | 4 | 3 | 12 | Self-approval rejected at SQL level (`requested_by<>$1`), 409 on race | 2 |
| H3 | Replay of an old approval | Broker message redelivered or resent | 3 | 3 | 9 | 5-min expiry enforced on edge, idempotency analysis required per driver, request_id dedupe at executor (required before merge) | 3 |
| H4 | Unapproved action type | Config injection or bug sends non-allowlisted action | 4 | 2 | 8 | Edge config allowlist, signed config (lifecycle gate), executor rejects unknown actions | 2 |
| H5 | Command storm / chattering actuator | Client loop or bug reissues commands | 3 | 3 | 9 | Approval required per command (no auto-approve), rate limits on API, executor must rate-limit + debounce per actuator (pre-merge requirement) | 3 |
| H6 | Fail-unsafe on comms loss | Gateway loses broker mid-sequence | 4 | 2 | 8 | No server-side retry (no blind retries), device interlocks + local fail-safe required per driver, watchdog returns actuator to safe state | 3 |
| H7 | Wrong device actuated | Device_id mixup, cross-tenant confusion | 4 | 2 | 8 | Tenant scoping on every query, per-gateway ACL subtrees (broker mTLS card), executor confirms device binding before write | 3 |
| H8 | Actuation during maintenance | Technician on equipment while remote command arrives | 5 | 2 | 10 | Local lockout/etag switch required (device interlock, software never sole layer), maintenance mode flag per site (pre-merge requirement) | 5 |
| H9 | MCP/AI-initiated actuation | Prompt injection drives a write | 4 | 2 | 8 | No MCP write tools exist; adding any requires separate safety + authorization review (security.md) | 2 |
| H10 | Approval fatigue | High command volume trains rubber-stamping | 3 | 3 | 9 | Approval UI must show full command + measured recent values (pre-merge requirement), audit sampling | 4 |

## 3. Actuator-class FMEA (template - instantiate per driver)

| Class | Failure mode | Effect | Detect | Required local interlock |
|-------|--------------|--------|--------|--------------------------|
| Relay/contactor | Welded contacts, chattering | Load stuck on, equipment damage | Feedback point on telemetry | Hardware watchdog, manual override |
| Motor / VFD | Over-speed, reverse while running | Mechanical damage | Speed feedback | Drive-level interlock, E-stop loop |
| Valve | Stuck mid-travel, water hammer | Process upset | Position feedback | Limit switches, pressure relief |
| Heater | Stuck on | Over-temperature, fire | Temperature point | Independent thermal cutout (non-software) |
| Door lock | Unlock during occupied hours | Security/safety incident | Door contact point | Schedule interlock, local egress always free |

## 4. Interlock review (standing requirements)

- Software is never the only safety layer: every actuator driver documents
  its hardware interlock and fail-safe state before merge.
- The edge executor must verify the command's `request_id` is fresh
  (dedupe), unexpired, allowlisted, and bound to a configured device.
- Any sensor interlock condition (e.g. temperature above threshold) polled
  by the driver must veto execution locally, not via a cloud round-trip.
- Maintenance mode: a per-site flag that disables command execution at the
  edge, set locally, reported in telemetry as a first-class state.

## 5. Open items before the first actuator executor merges

1. Wire approved-command publish to the broker cmd topic (currently TODO).
2. Edge executor with request_id dedupe, per-actuator debounce/rate limit.
3. ACK + measured-outcome publish back to `commands.status` (acked/failed).
4. Maintenance-mode interlock (edge-local, telemetry-visible).
5. Approval UI showing full command context + live values of the target.
6. This document updated with the actuator's row in the FMEA table,
   signed off by a reviewer other than the driver author.

## 6. Residual risk statement

With the executor unshipped, residual actuation risk is zero by absence.
After section 5 lands, the highest residual hazards are H8 (maintenance
lockout discipline, S5) and H1 (credential theft), both mitigated to
process-level controls and auditable. Reviewer sign-off: ____________
