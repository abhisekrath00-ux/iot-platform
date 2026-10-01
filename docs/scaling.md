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

- Telemetry is declared range-partitioned by month, but schema v1 only created a
  default partition, so until now all rows sat in one table. The leader-elected
  hourly job now creates the current and next 3 monthly partitions
  (`retention.EnsurePartitions`, tested). Months whose rows are already in the
  default partition are skipped and logged, because Postgres will not attach a
  partition over existing default rows; existing deployments keep old data in the
  default partition until an operator moves it. Indexed on (device, point, time desc).
- Hourly rollups so reports and long-range charts do not scan raw rows.
- Backpressure on HTTP ingest: body capped at 1 MB, batches capped at 500
  readings (413), per-key rate limit (`API_KEY_RPM`), duplicate event ids stored once.
- Stateless API and ingest processes behind a load balancer; leader election for
  the scheduler so only one replica runs scheduled jobs.

## Not built or not proven

- The API-key rate limit is per replica, so with N replicas the effective key limit is N times the setting. The response cache is per replica by default; `CACHE_BACKEND=redis` (with `REDIS_URL`) shares it, tested against a fake Redis only.
- Multi-replica ingest and first-match latch behaviour are not load-tested.
- Mosquitto runs as a single broker. For more than one broker use a clustered
  broker (EMQX or HiveMQ clusters, or Mosquitto behind a bridge per site) and keep
  the `t/<tenant>/g/<gateway>/...` topic scheme and ACL file identical on every node.
  This is guidance, not a tested configuration.
- Moving historical rows out of the default partition into monthly partitions is
  not automated.
