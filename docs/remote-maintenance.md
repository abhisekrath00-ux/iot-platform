# Remote maintenance: edge logs and agent restart

Settings for a box you cannot walk up to. Page: Fleet updates, card "Remote maintenance". Admin only.

## What it can do

- **Recent log lines.** Ask an edge box for the last 1 to 500 lines the agent itself logged since it started (default 200). Lines come from an in-memory buffer, never from other files. Values that follow names like password, token, secret, api key, authorization or community are hidden before the lines leave the box. This is a safety net, not a promise: the agent is not meant to log secrets.
- **Agent restart.** Restarts the agent program, not the machine. One admin requests, a different admin approves (an API key can never approve; the authenticator code is asked for when the workspace requires it). The request is valid for 5 minutes after approval.

## What it cannot do

No reboot, no shell, no file reads, no config changes, no firmware. Peripheral firmware, remote shell access and scheduled rollouts are separate things that are not built.

## Switches on the box

Restart only works when the box's own `edge-agent.yaml` has `remote_restart: true` (default off; the server cannot turn it on) and a service manager starts the agent again afterwards. The shipped Linux systemd units use `Restart=always`. Without one, a restart simply stops the agent.

## How it travels

Over the existing MQTT link: request on `t/<tenant>/g/<gateway>/ops`, answer on `.../ops/result`. The broker ACL gives each gateway read on its own `ops` and write on its own `ops/result` only; ingest stores a result only on an open operation of the same tenant and gateway. Requests carry an id, an issue and expiry time (5 minutes, never more than 10 on the box) and are accepted once. An operation is open for at most 10 minutes; one operation at a time per gateway.

The gateway trusts the request because of mutual TLS, the broker ACL, the expiry and replay checks and its own opt-in. The approver name in a restart request is for the audit trail, not a signature: the edge cannot check it. Signed requests are not built.

## Honest status

TESTED: edge request gate (expired, future, long lifetime, unknown kind or key, bad id, replay, restart needs the local opt-in and a named approver), log tail size caps and secret hiding; server API with a fake publisher against Postgres (admin only, validation, one open operation at a time, logs sent at once, restart held until a different admin approves, self-approval and viewer refused, expired approvals refused, list and expiry); UI type-checked and screenshotted with seeded rows.

NOT TESTED: a real edge box, a real broker (the ACL lines are generated, not exercised), the ingest result handler against a real MQTT message, a restart under systemd, Windows (the service manager may not restart the agent: check its recovery settings), authenticator-code approval in the UI.
