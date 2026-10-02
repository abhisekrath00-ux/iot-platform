# Time-series store: options, tradeoffs, plan

Status: evaluation and plan. Nothing here is migrated. The harness
(`server/cmd/tsbench`) exists and runs; no result from target hardware exists
yet, so do not quote a throughput figure. A smoke run on the dev sandbox
(144k rows, one machine) only proves the harness works.

## What we run today

Postgres with monthly range partitions on telemetry (`retention.EnsurePartitions`),
hourly rollups, optional raw purge, reports reading rollups for purged ranges.
Everything is tenant-scoped in SQL. One database, one backup story, works
air-gapped.

Built since the first draft (tested): stored daily rollups built from hourly,
per-tenant retention (`GET/PUT /v1/retention`, admin only, audited) for raw and
hourly data, daily rows used by day/week reports once hourly rows are purged.
Still open: the 24h series API reads raw rows, no compression of old raw data,
a read seam exists for report aggregation only (`internal/tsstore.Store.Aggregate`, Postgres implementation, conformance suite in `tsstore/storetest`). About 30 other call sites (ingest writes, latest values, anomalies, KPIs, rules, MCP) still use SQL directly, so no second engine can be swapped in yet.

## Options

### A. Hardened partitioned Postgres (recommended default)
- Add: stored daily rollups, per-tenant retention, BRIN or covering indexes
  per partition, detach and archive old partitions, a rollup that reads only
  new data, and read paths that choose raw vs hourly vs daily by range.
- Pros: no new component, no new licence, same backup and tenancy model,
  simplest air-gapped bundle.
- Cons: no columnar compression; storage per point stays larger; very high
  ingest (many tens of thousands of points per second sustained) needs
  careful batching and more hardware.

### B. TimescaleDB
Licence read from the official files on 2026-10-02:
https://raw.githubusercontent.com/timescale/timescaledb/main/tsl/LICENSE-TIMESCALE
and https://docs.timescale.com/about/latest/timescaledb-editions/
- The core is Apache 2.0. The advanced features (the editions page puts the
  newest features in "Community Edition") are under the Timescale License (TSL),
  free to run on your own infrastructure.
- TSL forbids offering it as database-as-a-service or as a service where the
  TSL software provides time-series functions to third parties, except as
  part of a "Value Added Product". A Value Added Product must be mainly
  something other than a database (an IoT platform is the licence's own
  example) and, in clause (iii), its users must be prohibited contractually or
  technically from defining or modifying the database schema.
- Risk for us: our users are developers and testers. If they get direct
  database access, clause (iii) is a question for a lawyer. This is a reading
  of the licence text, not legal advice. Do not bundle TSL features until the
  owner has had that reviewed.
- Pros: native compression, continuous aggregates, retention policies, plain
  SQL and Postgres tooling, so migration from option A is small.
- Cons: licence review, extra extension to ship and upgrade in the offline bundle.
- The Apache-only build exists but lacks the features that make it worth it.

### C. ClickHouse
Apache 2.0 (https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/LICENSE).
- Pros: columnar compression, very fast aggregates over billions of rows.
- Cons: a second database to run, back up, secure and patch; no foreign keys
  or transactional joins to our Postgres metadata, so tenant checks and joins
  move into the app; more operator skill; heavier in a small air-gapped site.
- Fits as an optional scale-out for sites past what Postgres handles, not as
  the default.

## Recommendation
1. Keep Postgres as the default store and harden it (option A).
2. Put telemetry reads and writes behind a `TelemetryStore` interface so the
   engine is a deployment choice, not a rewrite.
3. Measure first, then decide on B or C. Only if the benchmark shows a miss
   against the targets below, and, for B, only after licence review.

## Benchmark plan (not yet run)
Harness: `server/cmd/tsbench` (Postgres only today), one binary, same data generator for every engine.
- Load: N devices x M points, 1 s to 60 s intervals; ingest in batches.
- Measure: sustained ingest rows/s at p99 batch latency under 1 s; storage
  bytes per point after compression or rollups; query latency p50/p95 for
  24 h raw, 30 d hourly, 1 y daily, and a 50-device dashboard load; rollup
  job duration; restore time from backup.
- Targets (proposed, to be agreed): 10k points/s sustained on 4 vCPU,
  24 h query p95 under 300 ms, 1 y query p95 under 1 s.
- Report: a table with hardware, versions, seed and raw output committed.

## Rollback path
Any engine change ships behind a flag with dual-write: Postgres keeps
receiving every write, reads switch per tenant by flag, and flipping the flag
back is the rollback. Postgres is retired only after a full retention period
of clean dual-write plus a verified restore. No data is deleted by a switch.
