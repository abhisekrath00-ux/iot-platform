# TimescaleDB: licence and compatibility comparison (internal)

Status: research only. Nothing is installed, bundled or committed to. This is a
reading of public texts fetched on 2026-10-02, not legal advice. The owner
should have a lawyer confirm section 2 before any Timescale code ships.

## Sources read

- Editions page: https://docs.timescale.com/about/latest/timescaledb-editions/
- Licence text (TSL): https://raw.githubusercontent.com/timescale/timescaledb/main/tsl/LICENSE-TIMESCALE
- Version matrix: https://docs.timescale.com/self-hosted/latest/upgrades/upgrade-pg/
- Unique index rule: https://docs.timescale.com/use-timescale/latest/hypertables/hypertables-and-unique-indexes/
- Project README (columnstore, continuous aggregates, pg18 image): https://raw.githubusercontent.com/timescale/timescaledb/main/README.md

## Two editions

| | Apache 2 edition | Community edition (TSL) |
| --- | --- | --- |
| Licence | Apache 2.0, "completely unrestricted" per the editions page | Timescale/Tiger Data Licence |
| Hypertables, chunks, drop_chunks | yes | yes |
| Columnstore (compression) policies | no | yes |
| Continuous aggregate policies and refresh | no | yes |
| Retention policies (add_retention_policy) | no | yes |
| Run on your own servers for free | yes | yes, if the terms below hold |

The editions page lists the columnstore, continuous aggregate policy and
retention policy functions as Community only. Those are the features that would
replace our hand-built rollups and retention, so the Apache edition alone gives
little over what we already have.

## TSL terms that matter for us (section numbers from the licence text)

- 2.1(a) Internal use: allowed only if the DDL and DML interfaces are not
  exposed, "directly or indirectly (e.g., via a wrapper)", to anyone except you,
  your employees and contractors.
- 2.1(b) Value Added Product: allowed to ship the binaries inside a product,
  provided (1) customers are told Timescale use is under the TSL and given the
  licence text or URL, and (2) customers are prohibited, contractually or
  technically, from defining, redefining or modifying the database schema in
  that database.
- 3.10 defines a Value Added Product. The licence gives "an IoT platform" as an
  example, but all three must hold: not primarily a database product; adds
  substantial value of a different kind and is what is sold; and users cannot
  change the schema (clause iii).
- 2.2 forbids offering it as database-as-a-service, or any service where the TSL
  software offers time-series database functions to third parties, except as
  part of a Value Added Product.

## What this means for Hexmon

1. Our users are developers and testers. If any of them can run SQL against the
   database (psql, a SQL console, an MCP or API path that accepts raw SQL), the
   "prohibited from changing the schema" test in 3.10(iii) and 2.1(b)(2) needs
   to hold technically or by contract. Today the API exposes no raw SQL, and
   migrations run as the service account. A contract term would still be needed.
2. Air-gapped delivery means we would redistribute binaries (2.1(b)(iii)). That
   triggers the notice duty in 2.1(b)(1) and must be in the offline bundle.
3. The risk is not the free price. It is that customers who self-host with a
   superuser could break clause (iii), and that we would carry the notice and
   contract duties for each customer.
4. Open question for a lawyer: whether a customer admin who can reach the
   database host, but only through our app, counts as "technically prohibited".

## Compatibility with the current stack

- We run `postgres:16-alpine` (docker-compose.yml). The version matrix shows
  TimescaleDB 2.23 to 2.29 supporting PostgreSQL 16. 2.29.x drops PostgreSQL 15.
  The matrix page also says to avoid PostgreSQL 16.5 with TimescaleDB because
  of a reverted binary interface change (fixed in 16.6).
- The stock Alpine Postgres image has no Timescale. We would have to ship a
  Timescale-built image (the README names `timescale/timescaledb-ha:pg18`), which
  means a new image to mirror, scan and patch in the offline bundle.
- Our `telemetry` table is natively partitioned by month with primary key
  `(event_id, observed_at)`. Timescale's rule is that a unique index must contain
  all partitioning columns, and `observed_at` is already in ours, so the key is
  compatible. Converting an existing native-partitioned table to a hypertable is
  NOT confirmed from the pages above. Plan on a new hypertable and a copy-over
  migration, and test it on a restore of real data before trusting it. This is
  an unverified assumption.
- Our code reads telemetry through SQL in about 30 places; only report
  aggregation goes through `tsstore.Store`. A Timescale implementation of that
  seam is easy; the other call sites keep working because it is still Postgres.
- Not testable here: this sandbox's Postgres has no timescaledb extension
  (`pg_available_extensions` shows none), so no adapter has been written or run.

## Options if the licence review says no

- Stay on partitioned Postgres (current default, see timeseries-store.md).
- Use the Apache edition for hypertable chunking only, and keep our own rollups.
  Smaller gain, no TSL exposure.
- ClickHouse as an optional scale-out (Apache 2.0), as already noted.

## Recommendation

Do not adopt yet. Order of work if the owner wants to continue:
1. Run `tsbench` on target hardware against the current Postgres design.
2. Only if it misses the targets in timeseries-store.md, get legal review of
   TSL 2.1(b) and 3.10 against our actual deployment and support model.
3. Then build a Timescale `tsstore.Store` and run `storetest` against it.
