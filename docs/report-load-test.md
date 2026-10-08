# Report load check

`scripts/report-load.sh` seeds 2.4 million synthetic readings (4 devices, about 7 days at one reading a second), creates a report with 4 metrics grouped by hour over 168 hours, and runs it from 2 parallel clients for 20 seconds. It removes what it wrote. Use it on a test or lab stack only.

## Results (2026-10-08, this sandbox, same script run four times)

Machine: 2 CPUs, API and Postgres 14 on the same box, nothing else tuned. Default gate settings (4 reports at once, 2 per tenant).

| Check | Result |
|---|---|
| One report run over 2.4 million rows | about 1.0 to 1.1 s |
| 2 clients in a loop for 20 s, run A | 35 runs, all 200, p50 1.14 s, p95 1.22 s, max 1.61 s |
| Same, after a clean-up that left dead rows (no vacuum) | 6 runs, p50 8.7 s |
| Same, after VACUUM, run B | 20 runs, p50 2.1 s, p95 2.1 s |
| Same, run C | 32 runs, p50 1.2 s, p95 2.2 s |
| 20 requests at once from one tenant | 2 ran (about 1.3 s), 18 got HTTP 503 straight away |
| 8 requests at once from one tenant | 2 ran, 6 got HTTP 503 |

Read this as "about 1 to 2 seconds for a 4-metric, 7-day, 2.4 million row report on a small shared box when the table is vacuumed", and "several times slower when the table is full of dead rows". The spread between runs is real; no single number here is a promise. The script now runs VACUUM ANALYZE after seeding for that reason.

The 503s are the per-tenant limit working as designed (`REPORT_MAX_PER_TENANT`, default 2): the API refuses instead of queuing and starving other tenants. Clients should retry later.

Sandbox note: the Postgres build here cannot load its JIT library, so JIT is switched off for the test database. Production Postgres packages do not have this problem.

## What this does not show

- One machine, one run, one report shape. It is not a capacity figure for a customer's hardware.
- Only one tenant was loaded, so the fairness between tenants and the global cap of 4 were not exercised here (the gate has unit tests).
- Only the run endpoint was timed. PDF, XLSX, Word and PowerPoint downloads were not load tested.
- Data was synthetic, regular and in one partition. Real data across many partitions and points may differ.
- Memory use during runs was not measured.
