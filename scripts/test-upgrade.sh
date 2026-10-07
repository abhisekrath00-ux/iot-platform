#!/usr/bin/env bash
# Tests scripts/upgrade.sh with fake docker/curl programs and a real local git remote.
# No Docker is needed or touched. It checks preflight failures, the backup-first order,
# fast-forward updates, the up-to-date no-op, automatic rollback on failure, and --rollback.
# It does NOT prove the real compose stack upgrades; that has never been run on a real host.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin"

# ---- fake docker and curl ----------------------------------------------------------------------
cat > "$T/bin/docker" <<'D'
#!/usr/bin/env bash
echo "docker $*" >> "$FAKE_LOG"
case "$1 $2" in
  "info "*) [ "${FAKE_DOCKER_DOWN:-0}" = 1 ] && exit 1; exit 0 ;;
  "compose version") exit 0 ;;
  "compose stop"*|"compose start"*) exit 0 ;;
esac
if [ "$1" = compose ]; then
  case " $* " in
    *" pg_dump "*) echo "FAKE-DUMP"; exit 0 ;;
    *" pg_restore "*) cat > "$FAKE_RESTORED"; exit 0 ;;
    *" up "*) [ "${FAKE_UP_FAIL:-0}" = 1 ] && exit 1; exit 0 ;;
    *" load "*) cat > /dev/null; exit 0 ;;
  esac
fi
exit 0
D
cat > "$T/bin/curl" <<'C'
#!/usr/bin/env bash
[ "${FAKE_HEALTH_FAIL:-0}" = 1 ] && exit 1; exit 0
C
chmod +x "$T/bin/docker" "$T/bin/curl"
export PATH="$T/bin:$PATH" FAKE_LOG="$T/log" FAKE_RESTORED="$T/restored" HEXTHINGS_HEALTH_TRIES=2 HEXTHINGS_HEALTH_SLEEP=0
git config --global user.email test@example.com 2>/dev/null || true
git config --global user.name test 2>/dev/null || true
git config --global init.defaultBranch main 2>/dev/null || true

# ---- a real git remote with two revisions ------------------------------------------------------
git init -q --bare "$T/origin.git"
git clone -q "$T/origin.git" "$T/seed"
cp "$ROOT/docker-compose.yml" "$T/seed/"; echo v1 > "$T/seed/marker.txt"
printf '.env\nbackups/\n*.log\n.upgrade-state\n' > "$T/seed/.gitignore"
mkdir -p "$T/seed/scripts"; cp "$ROOT/scripts/backup.sh" "$ROOT/scripts/upgrade.sh" "$T/seed/scripts/"
(cd "$T/seed" && git add -A && git commit -qm rev1 && git push -q origin main)
REV1=$(git -C "$T/seed" rev-parse HEAD)

new_clone() { git clone -q "$T/origin.git" "$1"; mkdir -p "$1/scripts"; cp "$ROOT/scripts/backup.sh" "$ROOT/scripts/upgrade.sh" "$1/scripts/"; printf 'WEB_PORT=18432\n' > "$1/.env"; }
push_rev() { # push_rev <value> -> advances origin/main, echoes the new sha
  (cd "$T/seed" && echo "$1" > marker.txt && git add -A && git commit -qm "$1" && git push -q origin main)
  git -C "$T/seed" rev-parse HEAD
}

fails=0; check() { if ! eval "$2"; then echo "FAIL: $1"; fails=$((fails+1)); else echo "ok:   $1"; fi; }
run() { : > "$FAKE_LOG"; (cd "$W" && bash scripts/upgrade.sh --yes "$@" > "$T/out" 2>&1); }

# no .env -> refuses before touching anything
W="$T/w0"; git clone -q "$T/origin.git" "$W"; mkdir -p "$W/scripts"; cp "$ROOT/scripts/backup.sh" "$ROOT/scripts/upgrade.sh" "$W/scripts/"
run && rc=0 || rc=$?
check "no install (.env) is refused" '[ $rc -ne 0 ] && grep -q "Run scripts/install.sh first" "$T/out"'

# dirty tree -> refuses before any backup
W="$T/w1"; new_clone "$W"; echo dirty >> "$W/marker.txt"
run && rc=0 || rc=$?
check "uncommitted changes are refused before the backup" '[ $rc -ne 0 ] && grep -q "uncommitted changes" "$T/out" && ! grep -q "pg_dump" "$FAKE_LOG"'
git -C "$W" checkout -q -- marker.txt

# happy path: backup, fast-forward, restart, health, state file
REV2=$(push_rev v2)
run && rc=0 || rc=$?
check "upgrade exits 0" '[ $rc -eq 0 ]'
check "the backup ran before the update" 'grep -q "pg_dump" "$FAKE_LOG" && [ "$(grep -n "pg_dump" "$FAKE_LOG" | head -1 | cut -d: -f1)" -lt "$(grep -n "compose up" "$FAKE_LOG" | head -1 | cut -d: -f1)" ]'
check "checkout fast-forwarded to the new revision" '[ "$(git -C "$W" rev-parse HEAD)" = "$REV2" ] && [ "$(cat "$W/marker.txt")" = v2 ]'
check "state file records the previous revision and a real backup" 'grep -q "^PREV_REF=$REV1" "$W/.upgrade-state" && B=$(sed -n "s/^PREV_BACKUP=//p" "$W/.upgrade-state") && [ -f "$W/$B" ] && gunzip -c "$W/$B" | grep -q FAKE-DUMP'
check "source mode rebuilds" 'grep -q "compose up -d --build" "$FAKE_LOG"'

# up to date -> no-op, no second backup
run && rc=0 || rc=$?
check "already up to date is a no-op" '[ $rc -eq 0 ] && grep -q "already up to date" "$T/out" && ! grep -q "pg_dump" "$FAKE_LOG" && ! grep -q "compose up" "$FAKE_LOG"'

# apply failure -> automatic rollback to the previous revision and database
REV3=$(push_rev v3)
FAKE_UP_FAIL=1 run && rc=0 || rc=$?
check "a failed upgrade exits nonzero and says it rolled back" '[ $rc -ne 0 ] && grep -q "rolled back" "$T/out"'
check "rollback returns the checkout to the previous revision" '[ "$(git -C "$W" rev-parse HEAD)" = "$REV2" ] && [ "$(cat "$W/marker.txt")" = v2 ]'
check "rollback restores the pre-upgrade backup" '[ -f "$FAKE_RESTORED" ] && grep -q FAKE-DUMP "$FAKE_RESTORED" && grep -q "pg_restore" "$FAKE_LOG"'
unset FAKE_UP_FAIL 2>/dev/null || true   # VAR=x func leaks past the function in bash

# health failure -> also rolls back
REV4=$(push_rev v4)
: > "$FAKE_LOG"
FAKE_HEALTH_FAIL=1 run && rc=0 || rc=$?
check "an unhealthy upgrade rolls back too" '[ $rc -ne 0 ] && grep -q "did not become healthy" "$T/out" && [ "$(git -C "$W" rev-parse HEAD)" = "$REV2" ]'
unset FAKE_HEALTH_FAIL 2>/dev/null || true

# explicit --rollback after a good upgrade
(cd "$W" && bash scripts/upgrade.sh --rollback --yes > "$T/out" 2>&1) && rc=0 || rc=$?
check "--rollback returns to the revision the last good upgrade recorded" '[ $rc -eq 0 ] && [ "$(git -C "$W" rev-parse HEAD)" = "$REV1" ] && grep -q "healthy again" "$T/out"'

# bundle mode: checksums verified, images loaded, no git
W="$T/w2"; mkdir -p "$W/scripts"; cp "$ROOT/scripts/backup.sh" "$ROOT/scripts/upgrade.sh" "$W/scripts/"
cp "$ROOT/docker-compose.yml" "$W/"; printf 'WEB_PORT=18432\n' > "$W/.env"
echo fake-images | gzip > "$W/images.tar.gz"
(cd "$W" && sha256sum images.tar.gz > SHA256SUMS)
run && rc=0 || rc=$?
check "bundle mode verifies checksums and loads images" '[ $rc -eq 0 ] && grep -q "checksums verified" "$T/out" && grep -q "docker load" "$FAKE_LOG" && grep -q "compose up -d --no-build" "$FAKE_LOG"'
(cd "$W" && sha256sum images.tar.gz | sed 's/^./0/' > SHA256SUMS)
run && rc=0 || rc=$?
check "a damaged bundle is refused before the backup" '[ $rc -ne 0 ] && grep -q "checksum mismatch" "$T/out" && ! grep -q "pg_dump" "$FAKE_LOG" && ! grep -q "docker load" "$FAKE_LOG"'

[ $fails -eq 0 ] && echo "all upgrade checks passed" || { echo "$fails failed"; exit 1; }
