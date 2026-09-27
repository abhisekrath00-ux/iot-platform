#!/usr/bin/env bash
# Restores a Postgres backup produced by scripts/backup.sh.
#
#   ./scripts/restore.sh backups/hexmon-pg-<stamp>.dump.gz   full restore
#   ./scripts/restore.sh --verify <file>                     integrity + dry-run only
#
# Full restore REPLACES the current database contents (pg_restore --clean
# --if-exists). Stop the API and ingest first so no writes interleave:
#   docker compose stop api ingest
#   ./scripts/restore.sh <file>
#   docker compose start api ingest
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "${1:-}" = "--verify" ]; then
  F="${2:?usage: restore.sh --verify <file.dump.gz>}"
  echo "== checksum =="
  sha256sum "$F"
  grep -F "$(sha256sum "$F" | awk '{print $1}')" backups/MANIFEST >/dev/null \
    && echo "checksum matches MANIFEST" || echo "WARNING: not in MANIFEST (backup from another host?)"
  echo "== archive listing (dry run, nothing written) =="
  gunzip -c "$F" | pg_restore -l >/dev/null && echo "archive readable"
  exit 0
fi

F="${1:?usage: restore.sh <file.dump.gz>  (or --verify <file>)}"
[ -f "$F" ] || { echo "no such file: $F" >&2; exit 1; }

echo "== restoring $F into the running postgres =="
echo "    (expects api+ingest stopped: docker compose stop api ingest)"
gunzip -c "$F" | docker compose exec -T postgres pg_restore -U "${POSTGRES_USER:-iot}" -d "${POSTGRES_DB:-iot}" --clean --if-exists --no-owner
echo "== sanity: row counts =="
docker compose exec -T postgres psql -U "${POSTGRES_USER:-iot}" -d "${POSTGRES_DB:-iot}" -c \
  "SELECT 'telemetry' t, count(*) FROM telemetry UNION ALL SELECT 'devices', count(*) FROM devices UNION ALL SELECT 'flows', count(*) FROM flows UNION ALL SELECT 'audit_log', count(*) FROM audit_log;"
echo "restore complete. Start services: docker compose start api ingest"
echo "Then rebuild the search index from Postgres per docs/backup-restore.md."
