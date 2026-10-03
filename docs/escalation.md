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
- A step whose channel was deleted or disabled is skipped and logged.

Not built: on-call schedules and rotations, quiet hours, SMS or Teams. Tested with unit tests
for ordering and validation and an integration test against Postgres with a fake notifier (order, no repeat, ack stops it,
admin only, other tenant's channel refused). Not tested against real SMTP or Slack.
