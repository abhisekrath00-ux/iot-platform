# Optional telemetry index sink (OpenSearch / Elasticsearch)

**Off by default. Needs no internet.** It exists for sites that already run an OpenSearch or Elasticsearch
cluster (often on the same LAN) and want telemetry searchable there, for example in Kibana or OpenSearch
Dashboards. The platform never depends on it. Postgres (or the configured time-series store) stays the source
of truth and all charts, rules and reports read from it.

## Turn it on

Set on the API: `INDEX_SINK_URL=https://opensearch.plant.local:9200`. Optional:

| Variable | Meaning |
|---|---|
| `INDEX_SINK_INDEX` | Index name, lowercase, default `hexmon-telemetry` |
| `INDEX_SINK_USER` / `INDEX_SINK_PASSWORD` | HTTP basic auth |
| `INDEX_SINK_API_KEY` | `Authorization: ApiKey ...` (wins over basic auth) |
| `INDEX_SINK_ALLOW_INSECURE=1` | Allow plain `http` to a non-loopback host (credentials then travel unencrypted) |

The API refuses to start on a malformed URL or index name, and refuses plain `http` to a non-local host unless
you opt in. One replica runs the sink at a time (advisory lock, like the other background jobs).

## Behaviour

- Reads new rows from `telemetry` in `(received_at, event_id)` order and sends them with `_bulk`.
- `_id` is the event id, so retries overwrite and never duplicate. Delivery is at least once.
- The position is stored in `index_sink_cursor` and moves only after every item in a batch was accepted.
  A failure keeps the position, stores a short `last_error` and retries with backoff (up to 1 minute).
- Rows are sent once they are 5 seconds old, so a transaction that commits late is not skipped.
- Each document carries `tenant_id`, device, point, `@timestamp` (observed time), value, unit and quality.
- Nothing is read back from the index, and no user-facing endpoint queries it.

## Tenant isolation warning

The index holds every tenant's data. Put access control on the cluster (separate users or index patterns per
tenant, filtered on `tenant_id`) before you give anyone Kibana or Dashboards access. The platform cannot
enforce that for you.

## Honest status

- Tested: against a fake `_bulk` endpoint that implements the documented request and response format
  (cursor behaviour, failure and rejected-item handling, lag window, auth header, config validation).
  The rejected-item guard is sabotage-checked.
- **Not tested against a real OpenSearch or Elasticsearch.** No index template or mapping is created, so the
  cluster's dynamic mapping applies. Backfill starts from the beginning of the table on first run, which can be
  large; delete the `index_sink_cursor` row to restart.
- No retention sync: deleting telemetry here does not delete it from the index.
