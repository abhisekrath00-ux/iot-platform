# Scaling and measured limits

What is measured, what is designed, and what is not yet proven. Numbers come from
one shared 2.6 GHz Xeon sandbox VM with Postgres on the same host, so treat them
as a floor for a single process, not a capacity promise.

## Measured

| Path | How measured | Result |
|---|---|---|
| HTTP ingest handler plus Postgres (`POST /v1/telemetry/ingest`) | `go test ./cmd/api -run xxx -bench HTTPIngest -benchtime 100x`, batches of 100, alert rules and flows evaluated per reading | about 3,000 readings/s on one API process (1,770/s before batching the inserts) |
| MQTT ingest end to end | `cmd/loadtest` (see docs/slo.md) | not re-run in this pass, no broker in the sandbox. No current number is claimed. |

The remaining per-reading cost is alert-rule and flow evaluation, which query
Postgres for each reading. Raising it further means caching the rule set per
tenant (not built).

## Built

- Telemetry is range-partitioned by month, indexed on (device, point, time desc).
- Hourly rollups so reports and long-range charts do not scan raw rows.
- Backpressure on HTTP ingest: body capped at 1 MB, batches capped at 500
  readings (413), per-key rate limit (`API_KEY_RPM`), duplicate event ids stored once.
- Stateless API and ingest processes behind a load balancer; leader election for
  the scheduler so only one replica runs scheduled jobs.

## Not built or not proven

- The response cache and API-key rate limit are per replica. A shared (Redis)
  cache is not built. With N replicas the effective key limit is N times the setting.
- Multi-replica ingest and first-match latch behaviour are not load-tested.
- Mosquitto runs as a single broker. For more than one broker use a clustered
  broker (EMQX or HiveMQ clusters, or Mosquitto behind a bridge per site) and keep
  the `t/<tenant>/g/<gateway>/...` topic scheme and ACL file identical on every node.
  This is guidance, not a tested configuration.
- Table partition creation beyond the default partition is a manual operation
  today; check `docs/backup-restore.md` before large retention changes.
