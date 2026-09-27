# Backup and restore

## What holds state

| Store | Role | Backup strategy |
|---|---|---|
| Postgres | Source of truth: telemetry, devices, profiles, flows + versions, users, enrollment, audit | `scripts/backup.sh` nightly (cron/systemd timer), rotated |
| Elasticsearch | Derived search index over telemetry | Not backed up. Rebuilt from Postgres after a restore |
| `.env` + certs | Secrets, CA and broker certs | Operator's secrets process; NOT in these backups (by design - keep secrets out of backup files) |

## RPO / RTO targets

- **RPO (data loss window): 24 h** with the default nightly backup. Tighten to
  1 h by running `scripts/backup.sh` hourly (custom-format dumps are small and
  gzip well) or by enabling Postgres WAL archiving to a second disk.
- **RTO (time to restore service): under 30 minutes** for a single-host
  deployment - a pg_restore of a typical site database takes minutes; the API
  reapplies idempotent migrations on boot; search reindexes in the background.
  Measure your site's real number during the quarterly drill below and record
  it with the customer.

## Backup

```sh
set -a; . ./.env; set +a          # pick up POSTGRES_USER/POSTGRES_DB if customized
./scripts/backup.sh               # writes backups/hexmon-pg-<stamp>.dump.gz
BACKUP_KEEP=30 ./scripts/backup.sh # keep 30 rotations instead of 14
```

Each backup is gzip-compressed pg_dump custom format with a sha256 recorded
in `backups/MANIFEST`. Copy the backup directory off-box (site file server,
object storage) - a backup on the same disk is not a backup.

## Restore

```sh
./scripts/restore.sh --verify backups/hexmon-pg-<stamp>.dump.gz   # integrity, no writes
docker compose stop api ingest                                     # quiesce writers
./scripts/restore.sh backups/hexmon-pg-<stamp>.dump.gz             # clean restore
docker compose start api ingest                                    # migrations reapply, idempotent
```

After a restore, rebuild the search index from Postgres (the API logs when
search falls behind; a full reindex endpoint is on the backlog - until then,
search covers new data immediately and historical search catches up on
reindex).

## Quarterly restore drill (do this with the customer)

1. Restore the latest backup onto a staging host or a scratch database.
2. Verify row counts (the script prints telemetry/devices/flows/audit counts)
   and spot-check a known device and a recent report in the UI.
3. Time the whole run from "start restore" to "UI healthy": that is your
   measured RTO. Record it on the Trello card and with the customer.
4. Restore one backup that is at least 30 days old once a year to prove
   rotation doesn't hide corruption.

Air-gapped note: everything above runs on the platform host with no network.
Backups leave the box only via media the site already sanctions.
