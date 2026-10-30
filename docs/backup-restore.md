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

Each backup is gzip-compressed pg_dump custom format with a file-bound `.sha256`
sidecar beside it, plus historical logging in `MANIFEST`. New archive, sidecar and
manifest files use private owner-only permissions. `BACKUP_KEEP` must be a positive
integer. Rotation removes each archive and its sidecar together. Copy the backup directory off-box (site file server,
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

## Rehearsal record (2026-10-04)

The mechanism behind `scripts/backup.sh` and `restore.sh` (`pg_dump -Fc`, `pg_restore --clean --if-exists --no-owner`)
was rehearsed against a development Postgres 16.2 holding about 11,600 telemetry rows, 27,500 audit rows, 166
devices, 152 flows and 41 users. The dump restored into a fresh database with identical row counts for telemetry,
devices, flows, audit_log, users and the newer tables, and the API booted on the restored database (all
migrations re-ran cleanly) and served requests.

What this does not prove: the shell scripts themselves were not run (they call `docker compose exec`, and there is
no Docker in the development environment), and nothing was restored on a clean host or a different Postgres
build. Do a full rehearsal with the scripts on the pilot host before relying on them.

Lesson from the rehearsal: `pg_dump` must be the same major version as the server (a v14 client refused to dump a
v16 server). The scripts run it inside the Postgres container, which satisfies this; if you dump from the host,
match the version.


## Fail-closed integrity preflight (2026-10-30)

Both `--verify` and a full restore require the adjacent `<archive>.sha256` sidecar
with exactly one hash and the exact archive basename. The archive bytes must match
before any database command starts. Then a matching container `pg_restore -l`
must read the archive before a destructive restore can begin. Missing sidecars,
wrong names, mismatches and unreadable archives fail nonzero. Full restore uses
`--exit-on-error`; it is not atomic and can partially change the target if a later
restore step fails. Stop writers and restore to a scratch database first.

Copy the sidecar with the archive, including custom backup directories. Older
backups without sidecars are intentionally refused. Recover a trusted original
checksum from the original host/record and verify it independently before creating
a file-bound sidecar. Do not generate trust by hashing the suspect archive itself.
SHA-256 detects accidental corruption, not malicious replacement of both files;
protect/off-site-copy the backup and trusted checksum through separate controls.
Backups contain private user and device state; they are not encrypted by this
script. `.env`, keys/certificates, asset files and other non-Postgres volumes still
need their own protected backup/recovery plan. No new RPO/RTO guarantee is made.

Executed rehearsal: `bash scripts/test-backup-restore.sh` uses matching local
PostgreSQL 14.24 clients/server on disposable source/target databases through a
Docker-command shim. The actual scripts dump, verify and restore a marker row,
check private modes/custom directories, reject corrupt/missing/wrong-name and
checksum-matching unreadable archives before destructive restore, and rotate
sidecars with archives. This tests real database bytes and shell decisions, not
Docker execution, Windows, clean-host recovery or HA failover. Those gates remain.

## PowerShell recovery path (2026-10-30)

`hexthings backup` now uses unique container/host filenames, separates `pg_dump`
from compression so a failed dump cannot hide behind gzip success, writes a
file-bound ASCII `.sha256` sidecar, and rotates it with the archive. Temporary
container dump files are removed on completion/failure. File-write errors stop
rather than printing a successful backup. Protect the host backup folder with
Windows ACLs: this increment does not implement or verify Windows permission or
encryption policy.

`hexthings restore FILE` requires the same file-bound checksum before copying or
stopping services, decompresses and lists the archive before confirmation, checks
that writers stopped, and uses `pg_restore --exit-on-error`. A restore failure
returns nonzero and leaves writers stopped with a partial-database warning. Failed
service start/readiness is also nonzero, not a successful recovery. `-Yes` bypasses
confirmation only, never integrity or readiness gates. The restore is not atomic.

Verification: command-contract cases execute the actual two PowerShell function
bodies under PowerShell 7.6.5 on Linux with fake Docker, covering missing/wrong-name/
corrupt checksums; copy/decompression/list/stop/restore/start/readiness failures;
dump/compression/copy failures; successful sidecar output and unique backup paths.
Run `PWSH=/path/to/pwsh python3 scripts/test-recovery-powershell.py`. This is not
native Windows/PowerShell 5.1, Docker Desktop, real database or ACL proof. Linux
real-PG rehearsal remains separate. The runtime used for this local test was the
hash-checked official release: https://github.com/PowerShell/PowerShell/releases/tag/v7.6.5.
