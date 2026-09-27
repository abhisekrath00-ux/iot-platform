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
cd "$(dirname "$0")/.."
DIR="${1:-backups}"
KEEP="${BACKUP_KEEP:-14}"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$DIR"
OUT="$DIR/hexmon-pg-$STAMP.dump"

echo "== dumping postgres (custom format) =="
docker compose exec -T postgres pg_dump -U "${POSTGRES_USER:-iot}" -Fc "${POSTGRES_DB:-iot}" > "$OUT.tmp"
mv "$OUT.tmp" "$OUT"
gzip -9 "$OUT"
OUT="$OUT.gz"

# manifest + checksum for integrity verification at restore time
SIZE=$(stat -c%s "$OUT")
SHA=$(sha256sum "$OUT" | awk '{print $1}')
printf '%s  %s  %s bytes\n' "$STAMP" "$SHA" "$SIZE" >> "$DIR/MANIFEST"
echo "backup: $OUT ($SIZE bytes, sha256 $SHA)"

echo "== rotating (keep last $KEEP) =="
ls -1t "$DIR"/hexmon-pg-*.dump.gz 2>/dev/null | tail -n +$((KEEP+1)) | while read -r f; do
  rm -f "$f" && echo "rotated out: $f"
done
echo "done. verify any backup with: ./scripts/restore.sh --verify $OUT"
