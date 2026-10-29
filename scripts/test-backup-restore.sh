#!/usr/bin/env bash
# Real disposable Postgres databases through a Docker-command shim. No Docker
# install/volume/network/Windows/HA proof. Requires local matching pg clients.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PGHOST=${PGHOST:-127.0.0.1}; PGPORT=${PGPORT:-5433}; PGUSER=${PGUSER:-sandbox}
export PGHOST PGPORT PGUSER
T=$(mktemp -d); SRC="hx_backup_src_$$"; DST="hx_backup_dst_$$"
cleanup() { dropdb --if-exists "$SRC" >/dev/null 2>&1 || true; dropdb --if-exists "$DST" >/dev/null 2>&1 || true; rm -rf "$T"; }
trap cleanup EXIT
mkdir -p "$T/bin" "$T/custom backups"
export REAL_PGDUMP=$(command -v pg_dump) REAL_PGRESTORE=$(command -v pg_restore) REAL_PSQL=$(command -v psql)
cat > "$T/bin/docker" <<'SHIM'
#!/usr/bin/env bash
set -euo pipefail
[ "$1 $2 $3 $4 $5" = 'compose exec -T postgres pg_dump' ] && { shift 4; exec "$REAL_PGDUMP" "${@:2}"; }
[ "$1 $2 $3 $4 $5" = 'compose exec -T postgres pg_restore' ] && { shift 4; printf '%s\n' "$*" >> "$CALLS"; exec "$REAL_PGRESTORE" "${@:2}"; }
[ "$1 $2 $3 $4 $5" = 'compose exec -T postgres psql' ] && { shift 4; exec "$REAL_PSQL" "${@:2}"; }
echo "unsupported shim call" >&2; exit 2
SHIM
chmod +x "$T/bin/docker"
export PATH="$T/bin:$PATH" CALLS="$T/calls" POSTGRES_USER="$PGUSER"
createdb "$SRC"; createdb "$DST"
psql -d "$SRC" -v ON_ERROR_STOP=1 -qc "CREATE TABLE telemetry(v int); INSERT INTO telemetry VALUES(42); CREATE TABLE devices(v int); CREATE TABLE flows(v int); CREATE TABLE audit_log(v int);"
cd "$ROOT"
export POSTGRES_DB="$SRC"
BACKUP_KEEP=2 bash scripts/backup.sh "$T/custom backups" > "$T/backup.log"
F=$(find "$T/custom backups" -name '*.dump.gz' -print -quit)
[ -n "$F" ] && [ -f "$F.sha256" ]
[ "$(stat -c %a "$F")" = 600 ] && [ "$(stat -c %a "$F.sha256")" = 600 ]
echo 'ok: real dump with private archive/sidecar in custom directory'
export POSTGRES_DB="$DST"
bash scripts/restore.sh --verify "$F" > "$T/verify.log"
! grep -q -- '--clean' "$CALLS"
echo 'ok: valid verify lists without destructive restore'
: > "$CALLS"
cp "$F.sha256" "$T/saved"
printf x >> "$F"
if bash scripts/restore.sh "$F" > "$T/rejected.log" 2>&1; then echo 'FAIL corrupted archive accepted'; exit 1; fi
[ ! -s "$CALLS" ]; grep -q 'Checksum mismatch' "$T/rejected.log"
echo 'ok: corruption rejected before any restore process'
# Regenerate a trusted archive, not a checksum for the corrupted one.
export POSTGRES_DB="$SRC"
BACKUP_KEEP=2 bash scripts/backup.sh "$T/custom backups" > "$T/backup2.log"
F=$(find "$T/custom backups" -name '*.dump.gz' -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)
export POSTGRES_DB="$DST"
mv "$F.sha256" "$F.sha256.saved"
if bash scripts/restore.sh "$F" > "$T/rejected.log" 2>&1; then echo 'FAIL missing sidecar accepted'; exit 1; fi
[ ! -s "$CALLS" ]; grep -q 'Missing checksum' "$T/rejected.log"
mv "$F.sha256.saved" "$F.sha256"
printf '%064d  wrong.dump.gz\n' 0 > "$T/wrong"
cp "$F.sha256" "$T/good"; cp "$T/wrong" "$F.sha256"
if bash scripts/restore.sh --verify "$F" > "$T/rejected.log" 2>&1; then echo 'FAIL wrong filename accepted'; exit 1; fi
[ ! -s "$CALLS" ]; cp "$T/good" "$F.sha256"
echo 'ok: missing and wrong-name sidecars fail before database access'
bash scripts/restore.sh "$F" > "$T/restore.log"
[ "$(psql -d "$DST" -Atc 'select v from telemetry')" = 42 ]
grep -q -- '--exit-on-error' "$CALLS"
echo 'ok: actual archive restores into fresh database with marker42'
if BACKUP_KEEP=0 bash scripts/backup.sh "$T/custom backups" > "$T/keep.log" 2>&1; then echo 'FAIL zero retention'; exit 1; fi
# A matching checksum is necessary but not sufficient: listing must succeed.
BAD="$T/custom backups/invalid.dump.gz"
printf 'not a postgres archive' | gzip > "$BAD"
(cd "$(dirname "$BAD")" && sha256sum "$(basename "$BAD")") > "$BAD.sha256"
: > "$CALLS"
if bash scripts/restore.sh "$BAD" > "$T/rejected.log" 2>&1; then echo 'FAIL unreadable archive accepted'; exit 1; fi
! grep -q -- '--clean' "$CALLS"
rm "$BAD" "$BAD.sha256"
echo 'ok: checksum-matching invalid archive never reaches destructive restore'
export POSTGRES_DB="$SRC"
BACKUP_KEEP=1 bash scripts/backup.sh "$T/custom backups" > "$T/rotation.log"
[ "$(find "$T/custom backups" -name '*.dump.gz' | wc -l)" -eq 1 ]
[ "$(find "$T/custom backups" -name '*.dump.gz.sha256' | wc -l)" -eq 1 ]
echo 'ok: rotation removes archive and matching sidecar together'
echo 'PASS: local PostgreSQL backup/restore shim rehearsal, not Docker/Windows/HA'
