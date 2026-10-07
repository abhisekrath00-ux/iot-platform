#!/usr/bin/env bash
# Safe in-place upgrade for an existing HexThings install.
#
#   ./scripts/upgrade.sh [--yes] [--force]     check, back up, update, restart, health check
#   ./scripts/upgrade.sh --rollback [--yes]    go back to the revision and database recorded by
#                                              the last upgrade (.upgrade-state)
#
# Order of operations: preflight (cheap checks first), a fresh Postgres backup via
# scripts/backup.sh, the source or image update, `docker compose up`, and a health check. If
# anything after the backup fails, the previous state is restored automatically: the old
# revision is checked out, the pre-upgrade backup is restored into Postgres, services are
# restarted, and the health check runs again. The final message always says which happened.
#
# Source checkouts update with git (fast-forward only). Air-gapped bundle installs update from a
# new bundle placed next to this script (images.tar.gz + SHA256SUMS, verified before anything is
# loaded). No internet is needed for the bundle path; everything is logged to upgrade.log.
set -euo pipefail
cd "$(dirname "$0")"
[ -f docker-compose.yml ] || cd ..
LOG=upgrade.log
: > "$LOG"

YES=0; FORCE=0; ROLLBACK=0
while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) YES=1 ;;
    --force) FORCE=1 ;;
    --rollback) ROLLBACK=1 ;;
    --help|-h) sed -n 2,16p "$0"; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
  shift
done

COLOR=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then COLOR=1; fi
if [ "$COLOR" = 1 ]; then G=$'\033[32m'; Y=$'\033[33m'; R=$'\033[31m'; N=$'\033[0m'; else G=; Y=; R=; N=; fi
plain() { printf '%s\n' "$(printf '%s' "$*" | sed $'s/\033\\[[0-9;]*m//g')" >> "$LOG"; }
say()  { printf '%s\n' "$*"; plain "$*"; }
ok()   { say "  ${G}ok${N}   $*"; }
warn() { say "  ${Y}warn${N} $*"; }
die()  { say "  ${R}FAIL${N} $*"; exit 1; }
confirm() {
  [ "$YES" = 1 ] && return 0
  [ -t 0 ] || return 0
  local a; read -r -p "  ? $1 [y/N]: " a || true
  case "$a" in y|Y|yes) return 0 ;; *) say "  cancelled; nothing was changed"; exit 0 ;; esac
}

WEB_PORT=$( [ -f .env ] && sed -n 's/^WEB_PORT=//p' .env | head -1 || true ); WEB_PORT="${WEB_PORT:-8080}"
http_ok() { if command -v curl >/dev/null 2>&1; then curl -fsS -m 3 "http://localhost:${WEB_PORT}/healthz" >/dev/null 2>&1; else wget -q -T 3 -O /dev/null "http://localhost:${WEB_PORT}/healthz" 2>/dev/null; fi; }
wait_health() { local _; for _ in $(seq 1 "${HEXTHINGS_HEALTH_TRIES:-60}"); do http_ok && return 0; sleep "${HEXTHINGS_HEALTH_SLEEP:-2}"; done; return 1; }
compose_args() { if [ -f models/model.gguf ]; then echo "--profile ai"; fi; }
# shellcheck disable=SC2086
up_stack() { docker compose $(compose_args) up -d ${UP_BUILD:---build} >> "$LOG" 2>&1; }
restore_db() { # restore_db <backup-file>: stop writers, restore, start writers.
  docker compose stop api ingest >> "$LOG" 2>&1 || warn "could not stop api/ingest cleanly (see $LOG); continuing the restore"
  gunzip -c "$1" | docker compose exec -T postgres pg_restore -U "${POSTGRES_USER:-iot}" -d "${POSTGRES_DB:-iot}" --clean --if-exists --no-owner >> "$LOG" 2>&1
  docker compose start api ingest >> "$LOG" 2>&1 || true
}
do_rollback() { # do_rollback <prev-ref-or-empty> <backup-file>
  local prev=$1 backup=$2
  say "  rolling back to the pre-upgrade state..."
  if [ -n "$prev" ] && [ -d .git ]; then
    git reset --hard "$prev" >> "$LOG" 2>&1 || die "rollback failed at 'git reset --hard $prev' (see $LOG). The backup is at $backup; restore it with scripts/restore.sh once the checkout is fixed."
  fi
  restore_db "$backup" || die "rollback failed while restoring the database from $backup (see $LOG). The backup file is intact; run scripts/restore.sh $backup once the database container is up."
  UP_BUILD=--build; up_stack || warn "rollback restarted services but compose reported an error (see $LOG)"
  if wait_health; then ok "rolled back${prev:+ to ${prev:0:12}}; the platform is healthy again"
  else warn "rolled back, but the health check still fails. Look at: docker compose logs api  (and $LOG)"; fi
}

# ---- cheap preflight: fail before anything changes --------------------------------------------
command -v docker >/dev/null 2>&1 || die "Docker is not installed. Install it, or fix the existing install with scripts/install.sh."
docker info >/dev/null 2>&1 || die "Docker is not running. Start Docker and run this again; nothing was changed."
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is missing ('docker compose version' fails)."
[ -f .env ] || die "no .env here: this folder has no install to upgrade. Run scripts/install.sh first."
ok "Docker and an existing install found"

BUNDLE=0
if [ -f images.tar.gz ] && [ ! -d .git ]; then BUNDLE=1; fi
PREV_REF=""; TARGET_REF=""
if [ "$ROLLBACK" = 0 ]; then
  if [ "$BUNDLE" = 1 ]; then
    [ -f SHA256SUMS ] || die "images.tar.gz is here but SHA256SUMS is missing. A bundle without checksums is not trusted; re-copy the whole bundle. Nothing was changed."
    (sha256sum -c SHA256SUMS >> "$LOG" 2>&1 || shasum -a 256 -c SHA256SUMS >> "$LOG" 2>&1) || die "bundle checksum mismatch: the bundle is damaged or was changed. Nothing was changed."
    ok "bundle checksums verified"
  else
    [ -d .git ] || die "not a git checkout and no bundle found. Update the files first (new bundle, or a checkout), then run this again."
    git rev-parse --git-dir >/dev/null 2>&1 || die ".git is damaged; fix the checkout and try again."
    [ -z "$(git status --porcelain)" ] || die "this checkout has uncommitted changes, and an upgrade would lose them. Commit or stash them, then run this again. Nothing was changed."
    PREV_REF=$(git rev-parse HEAD)
    git fetch origin >> "$LOG" 2>&1 || die "git fetch failed (offline?). For an air-gapped update use a bundle; nothing was changed."
    TARGET_REF=$(git rev-parse '@{u}' 2>/dev/null) || die "this branch has no upstream to fast-forward to. Set one, or update from a bundle."
    if [ "$TARGET_REF" = "$PREV_REF" ] && [ "$FORCE" = 0 ]; then
      ok "already up to date (${PREV_REF:0:12}); nothing to do (use --force to rebuild anyway)"
      exit 0
    fi
    say "  update: ${PREV_REF:0:12} -> ${TARGET_REF:0:12}"
  fi
fi

# ---- rollback-only path ------------------------------------------------------------------------
if [ "$ROLLBACK" = 1 ]; then
  [ -f .upgrade-state ] || die "no .upgrade-state: there is no recorded upgrade to roll back."
  PREV_REF=$(sed -n 's/^PREV_REF=//p' .upgrade-state | head -1)
  PREV_BACKUP=$(sed -n 's/^PREV_BACKUP=//p' .upgrade-state | head -1)
  [ -n "$PREV_BACKUP" ] && [ -f "$PREV_BACKUP" ] || die ".upgrade-state points at ${PREV_BACKUP:-nothing}, which is gone. Restore any backup with scripts/restore.sh <file>."
  say "HexThings rollback: back to ${PREV_REF:-the previous images}, database from $PREV_BACKUP"
  confirm "Replace the current install and database with that state?"
  do_rollback "$PREV_REF" "$PREV_BACKUP"
  exit 0
fi

# ---- backup first -----------------------------------------------------------------------------
say "HexThings upgrade"
BACKUP_DIR="${BACKUP_DIR:-backups}"
before=$(ls -1t "$BACKUP_DIR"/hexmon-pg-*.dump.gz 2>/dev/null | head -1 || true)
say "  backing up the database before anything changes..."
if ! bash scripts/backup.sh "$BACKUP_DIR" >> "$LOG" 2>&1; then
  die "the pre-upgrade backup failed (see $LOG). Nothing was changed. Fix the backup first - do not upgrade without one."
fi
BACKUP_FILE=$(ls -1t "$BACKUP_DIR"/hexmon-pg-*.dump.gz 2>/dev/null | head -1 || true)
[ -n "$BACKUP_FILE" ] && [ "$BACKUP_FILE" != "$before" ] || die "backup.sh reported success but produced no new backup file. Nothing was changed."
ok "backup: $BACKUP_FILE"

confirm "Apply the update now? The platform restarts and is briefly unavailable."

# ---- apply + verify, roll back on failure ------------------------------------------------------
if [ "$BUNDLE" = 0 ]; then
  git merge --ff-only "$TARGET_REF" >> "$LOG" 2>&1 || die "could not fast-forward to ${TARGET_REF:0:12}: local history diverged. Resolve it by hand; the source was not changed (the backup at $BACKUP_FILE stays)."
fi
set +e
apply_rc=0
if [ "$BUNDLE" = 1 ]; then
  UP_BUILD=--no-build
  gunzip -c images.tar.gz | docker load >> "$LOG" 2>&1 || apply_rc=$?
else
  UP_BUILD=--build
fi
if [ "$apply_rc" = 0 ]; then up_stack || apply_rc=$?; fi
health_rc=1
if [ "$apply_rc" = 0 ]; then wait_health && health_rc=0; fi
set -e
if [ "$apply_rc" != 0 ] || [ "$health_rc" != 0 ]; then
  [ "$apply_rc" != 0 ] && warn "the update failed while loading or starting services (see $LOG)"
  [ "$apply_rc" = 0 ] && warn "the updated platform did not become healthy within 2 minutes (see $LOG)"
  do_rollback "$PREV_REF" "$BACKUP_FILE"
  die "upgrade failed and was rolled back. The platform runs the previous version again."
fi

{
  echo "STAMP=$(date -u +%Y%m%dT%H%M%SZ)"
  echo "PREV_REF=$PREV_REF"
  echo "PREV_BACKUP=$BACKUP_FILE"
} > .upgrade-state
ok "upgrade complete and healthy (undo with: $0 --rollback)"
say "  note: --rollback returns to the revision and database from BEFORE this upgrade."
