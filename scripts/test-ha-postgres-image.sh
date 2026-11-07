#!/usr/bin/env bash
# Disposable native-runner PostgreSQL persistence check, never a Patroni/HA drill.
set -euo pipefail
if [[ $# != 1 || -z $1 ]]; then
  echo 'usage: bash scripts/test-ha-postgres-image.sh LOCAL_IMAGE' >&2
  exit 2
fi
image=$1
command -v docker >/dev/null
scratch=$(mktemp -d)
name=hexthings-pg-smoke-${scratch##*/}
rmdir "$scratch"
volume=$name-data
cleanup() {
  status=$?
  trap - EXIT INT TERM
  docker rm -f "$name" >/dev/null 2>&1 || true
  if ! docker volume rm "$volume" >/dev/null; then
    echo "could not remove disposable volume $volume" >&2
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker volume create --label hexthings.disposable=postgres-image-smoke "$volume" >/dev/null
for phase in initialize reopen crash recover; do
  docker run --rm -i --name "$name" --network none \
    --mount "type=volume,source=$volume,target=/var/lib/postgresql/data" \
    --entrypoint /bin/sh "$image" -s -- "$phase" <<'CONTAINER'
set -eu
phase=$1
export LC_ALL=C
test "$(id -un)" = postgres
test "$(id -u)" != 0
test "$PGDATA" = /var/lib/postgresql/data
test -d "$PGDATA"
test -w "$PGDATA"
# Trust is only for this synthetic, network-disabled, disposable fixture.
if [ "$phase" = initialize ]; then
  test -z "$(ls -A "$PGDATA")"
  initdb -D "$PGDATA" --no-locale --auth=trust > /tmp/initdb.log
fi
test "$(cat "$PGDATA/PG_VERSION")" = 16
chmod 700 "$PGDATA"
finish() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" != 0 ]; then cat /tmp/initdb.log /tmp/postgres.log 2>/dev/null || true; fi
  if pg_ctl -D "$PGDATA" status >/dev/null 2>&1; then
    pg_ctl -D "$PGDATA" -m fast -w -t 30 stop || status=1
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
pg_ctl -D "$PGDATA" -l /tmp/postgres.log -o '-c listen_addresses= -c unix_socket_directories=/tmp' -w -t 30 start
if [ "$phase" = initialize ]; then
  psql -X -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres <<'SQL'
CREATE TABLE image_checkpoint (id integer PRIMARY KEY, value text NOT NULL);
INSERT INTO image_checkpoint VALUES (1, 'synthetic-volume-checkpoint');
SQL
fi
test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c 'SHOW server_version_num')" -ge 160000
expected_count=1
if [ "$phase" = recover ]; then expected_count=2; fi
test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c 'SELECT count(*) FROM image_checkpoint')" = "$expected_count"
test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c 'SELECT value FROM image_checkpoint WHERE id = 1')" = synthetic-volume-checkpoint
if [ "$phase" = crash ]; then
  # Confirm the ordinary durability settings rather than assuming defaults.
  for setting in fsync synchronous_commit full_page_writes; do
    test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c "SHOW $setting")" = on
  done
  psql -X -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c "INSERT INTO image_checkpoint VALUES (2, 'synthetic-before-immediate-stop')"
  # Keep row3 in a live transaction. The marker is emitted only after INSERT.
  psql -X -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres > /tmp/uncommitted.log 2>&1 <<'SQL' &
BEGIN;
INSERT INTO image_checkpoint VALUES (3, 'synthetic-uncommitted');
\echo uncommitted-insert-ready
SELECT pg_sleep(60);
ROLLBACK;
SQL
  transaction_pid=$!
  attempts=0
  until grep -Fx 'uncommitted-insert-ready' /tmp/uncommitted.log >/dev/null; do
    kill -0 "$transaction_pid"
    attempts=$((attempts + 1))
    test "$attempts" -le 100
    sleep 0.1
  done
  # A different connection must not see the open transaction's row.
  test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c 'SELECT count(*) FROM image_checkpoint WHERE id = 3')" = 0
  kill -0 "$transaction_pid"
  pg_ctl -D "$PGDATA" -m immediate -w -t 30 stop
  if wait "$transaction_pid"; then
    echo 'open transaction unexpectedly completed before immediate stop' >&2
    exit 1
  fi
  grep -E 'terminating connection|server closed the connection' /tmp/uncommitted.log
  # An immediate stop must leave a crash state, not a clean-shutdown fixture.
  pg_controldata "$PGDATA" | grep -E '^Database cluster state: +in production$'
else
  if [ "$phase" = recover ]; then
    test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c 'SELECT value FROM image_checkpoint WHERE id = 2')" = synthetic-before-immediate-stop
    test "$(psql -X -A -t -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres -c 'SELECT count(*) FROM image_checkpoint WHERE id = 3')" = 0
    printf 'PostgreSQL16 recovered committed rows and excluded uncommitted row\n'
    grep -F 'database system was interrupted' /tmp/postgres.log
    grep -F 'redo starts at' /tmp/postgres.log
  fi
  pg_ctl -D "$PGDATA" -m fast -w -t 30 stop
fi
printf 'PostgreSQL16 disposable volume phase passed: %s\n' "$phase"
CONTAINER
done
printf 'PostgreSQL16 non-root clean-restart and immediate-stop recovery passed\n'
