#!/usr/bin/env bash
# Backs up the platform's source of truth (Postgres) to a local, rotated
# backup directory. Runs on the platform host; no network access needed.
#
#   ./scripts/backup.sh [backup-dir]      default: ./backups
#
# Postgres holds all durable state (telemetry, devices, flows, users, audit).
# Elasticsearch is a derived search index and is rebuilt from Postgres after
# a restore - see docs/backup-restore.md.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
selected_compose() {
  if [ -e .secure-deployment ] || [ -L .secure-deployment ]; then
    [ -f scripts/selected-compose.py ] && command -v python3 >/dev/null 2>&1 || { echo "Secure deployment selector unavailable; refusing lab fallback." >&2;return 1; }
    python3 scripts/selected-compose.py "$PWD" "$@"
  else docker compose "$@";fi
}
DIR="${1:-backups}"
KEEP="${BACKUP_KEEP:-14}"
case "$KEEP" in ''|*[!0-9]*) echo "BACKUP_KEEP must be a positive integer" >&2; exit 2 ;; esac
[ "$KEEP" -gt 0 ] || { echo "BACKUP_KEEP must be at least 1" >&2; exit 2; }
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$DIR"
OUT="$DIR/hexmon-pg-$STAMP.dump"
# two backups started in the same second must not clobber each other
[ -e "$OUT" ] || [ -e "$OUT.gz" ] && OUT="$DIR/hexmon-pg-$STAMP-$$.dump"

trap 'rm -f "${OUT}.tmp"' EXIT

echo "== dumping postgres (custom format) =="
selected_compose exec -T postgres pg_dump -U "${POSTGRES_USER:-iot}" -Fc "${POSTGRES_DB:-iot}" > "$OUT.tmp"
mv "$OUT.tmp" "$OUT"
gzip -9 "$OUT"
OUT="$OUT.gz"

# Bind integrity to the exact archive name, in its own directory. Publish the
# sidecar atomically only after the archive is complete. The manifest is history.
(cd "$DIR" && sha256sum "$(basename "$OUT")") > "$OUT.sha256.tmp"
mv "$OUT.sha256.tmp" "$OUT.sha256"
# manifest + checksum for historical logging
SIZE=$(stat -c%s "$OUT")
SHA=$(sha256sum "$OUT" | awk '{print $1}')
printf '%s  %s  %s bytes\n' "$STAMP" "$SHA" "$SIZE" >> "$DIR/MANIFEST"
echo "backup: $OUT ($SIZE bytes, sha256 $SHA)"

echo "== rotating (keep last $KEEP) =="
ls -1t "$DIR"/hexmon-pg-*.dump.gz 2>/dev/null | tail -n +$((KEEP+1)) | while read -r f; do
  rm -f "$f" "$f.sha256" && echo "rotated out: $f"
done
echo "done. verify any backup with: ./scripts/restore.sh --verify $OUT"
