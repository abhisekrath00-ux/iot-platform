#!/usr/bin/env bash
# Restores a Postgres backup produced by scripts/backup.sh.
#
#   ./scripts/restore.sh backups/hexmon-pg-<stamp>.dump.gz   full restore
#   ./scripts/restore.sh --verify <file>                     integrity + dry-run only
#
# Full restore REPLACES the current database contents (pg_restore --clean
# --if-exists). Stop the API and ingest first so no writes interleave:
#   python3 scripts/selected-compose.py "$PWD" stop api ingest
#   ./scripts/restore.sh <file>
#   python3 scripts/selected-compose.py "$PWD" start api ingest
set -euo pipefail
cd "$(dirname "$0")/.."
selected_compose() {
  if [ -e .secure-deployment ] || [ -L .secure-deployment ]; then
    [ -f scripts/selected-compose.py ] && command -v python3 >/dev/null 2>&1 || { echo "Secure deployment selector unavailable; refusing lab fallback." >&2;return 1; }
    python3 scripts/selected-compose.py "$PWD" "$@"
  else docker compose "$@";fi
}

VERIFY=0
if [ "${1:-}" = "--verify" ]; then VERIFY=1; shift; fi
F="${1:?usage: restore.sh [--verify] <file.dump.gz>}"
[ "$#" -eq 1 ] || { echo "usage: restore.sh [--verify] <file.dump.gz>" >&2; exit 2; }
[ -f "$F" ] || { echo "no such file: $F" >&2; exit 1; }
[ -f "$F.sha256" ] || { echo "Missing checksum sidecar: $F.sha256. Refusing restore; recover the trusted checksum with the backup, never derive trust from the archive being checked." >&2; exit 1; }

echo "== file-bound checksum =="
# Do not let a supplied checksum file choose a different file or extra targets.
expected=$(awk -v name="$(basename "$F")" 'NR==1 && NF==2 && $2==name && length($1)==64 && $1 !~ /[^0-9a-f]/ {print $1}' "$F.sha256")
[ "$(wc -l < "$F.sha256")" -eq 1 ] && [ -n "$expected" ] || { echo "Invalid or mismatched checksum sidecar; refusing restore." >&2; exit 1; }
actual=$(sha256sum "$F" | awk '{print $1}')
[ "$actual" = "$expected" ] || { echo "Checksum mismatch; refusing restore." >&2; exit 1; }
echo "checksum matches archive name and bytes"
echo "== archive listing (nothing written) =="
# Use the matching container client, not an arbitrary host pg_restore version.
gunzip -c "$F" | selected_compose exec -T postgres pg_restore -l >/dev/null
echo "archive readable"
[ "$VERIFY" = 0 ] || exit 0

echo "== restoring $F into the running postgres =="
echo "    (expects api+ingest stopped through the verified deployment selector)"
gunzip -c "$F" | selected_compose exec -T postgres pg_restore -U "${POSTGRES_USER:-iot}" -d "${POSTGRES_DB:-iot}" --clean --if-exists --no-owner --exit-on-error
echo "== sanity: row counts =="
selected_compose exec -T postgres psql -U "${POSTGRES_USER:-iot}" -d "${POSTGRES_DB:-iot}" -c \
  "SELECT 'telemetry' t, count(*) FROM telemetry UNION ALL SELECT 'devices', count(*) FROM devices UNION ALL SELECT 'flows', count(*) FROM flows UNION ALL SELECT 'audit_log', count(*) FROM audit_log;"
echo 'restore complete. Start only api/ingest through: python3 scripts/selected-compose.py "$PWD" start api ingest'
echo "Then rebuild the search index from Postgres per docs/backup-restore.md."
