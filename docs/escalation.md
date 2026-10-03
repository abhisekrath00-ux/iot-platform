# Alert escalation

If an alert stays open and unacknowledged, more channels are notified after set delays.

- Policy per tenant, admin only: `GET/PUT /v1/escalation`, or the Escalation card on the Alerts page.
- A step is: severity (empty means any), step number, minutes since the alert was raised, and one of the tenant's enabled
  notification channels (email, Slack, webhook, Kafka, AMQP).
- Up to 5 steps per severity, 15 in total. Steps are numbered 1, 2, 3 without gaps and each delay must be longer than the
  one before. If a tenant defines steps for a severity, those replace the "any" steps for that severity.
- A sweep runs once a minute on one replica (leader lock). It sends at most one step per alert per sweep, in order, so a
  long outage escalates step by step and never skips.
- Acknowledging or resolving an alert stops the chain. The step is recorded on the alert (`escalation_level`) before it is
  sent, so a crash can lose one notification but never repeat one. Messages read "ESCALATION step N (unacknowledged for M
  min): ...". Webhook-style channels receive the event `alert.escalated`.
- Remind until acknowledged (optional, off by default, `repeat` in the same API): once an alert's last step has been
  sent, that step's channel is sent a reminder every N minutes (5-1440), at most M times (1-10) per alert. An
  acknowledge or resolve stops it. Reminders read "ESCALATION REMINDER k of M, step N ...". Leaving `repeat` out of a PUT
  keeps the current setting. The reminder is recorded (`escalation_repeats`) before it is sent, like a step.
- Quiet hours (optional, off by default, `quiet` in the same API): a daily window `start`-`end` as HH:MM in an IANA
  timezone (overnight windows such as 22:00-06:00 work). During it, reminders for non-critical alerts are held back and go
  out after it ends if the alert is still unacknowledged. Escalation steps and critical alerts are never held back,
  because delaying a first escalation or a critical page is not a safe default. Leaving `quiet` out of a PUT keeps the
  setting; empty start/end clear it. The zone database is compiled into the server, so it works in air-gapped images.
- A step whose channel was deleted or disabled is skipped and logged.

Not built: on-call schedules and rotations, SMS or Teams. Tested with unit tests
for ordering and validation and an integration test against Postgres with a fake notifier (order, no repeat, ack stops it,
admin only, other tenant's channel refused). Not tested against real SMTP or Slack.

## Maintenance windows

`POST /v1/maintenance` (admin, operator) starts a window on one device or one asset (it covers the devices under
that asset, including sub-assets) for up to 7 days; `POST /v1/maintenance/{id}/end` ends it early; `GET /v1/maintenance`
lists recent ones. Every change is audited.

- While a window covers a device, new warning and info alerts from rules are recorded with `shelved: true` and no
  notification is sent. They stay visible on the Alerts page with a "shelved" mark.
- Critical alerts are never shelved. A planned job does not get to silence a critical alarm.
- Shelved alerts are skipped by escalation. When no window covers the device any more and the alert is still open,
  one "still active after maintenance" notice goes out, the flag clears, and escalation applies from then on.
- Not covered: alerts raised by flows, and edge-local rules (the edge does not know about server windows).
  Tested with integration tests only; no real email or Slack.
