#!/usr/bin/env bash
# Synthetic entrypoint/restart and asynchronous replica fixture. No HA claim.
set -euo pipefail
if [[ $# != 2 || -z $1 || ! -x $2 ]]; then
  echo 'usage: bash scripts/test-patroni-image-bootstrap.sh LOCAL_IMAGE ETCD_BINARY' >&2
  exit 2
fi
image=$1
etcd_binary=$(realpath "$2")
work=$(mktemp -d)
chmod 755 "$work"
name=hexthings-patroni-${work##*/}
network=$name-net
volume=$name-data
replica=$name-replica
replica_volume=$name-replica-data
etcd=$name-etcd
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [[ $status != 0 ]]; then
    docker logs "$name" 2>&1 || true
    docker logs "$etcd" 2>&1 || true
    docker logs "$replica" 2>&1 || true
  fi
  docker rm -f -v "$name" "$replica" "$etcd" >/dev/null 2>&1 || true
  docker volume rm "$volume" "$replica_volume" >/dev/null || status=1
  docker network rm "$network" >/dev/null || status=1
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cat > "$work/patroni.yml" <<'YAML'
scope: disposable-image-bootstrap
namespace: /fixture/
name: fixture-a
restapi:
  listen: 0.0.0.0:8008
  connect_address: fixture-a:8008
etcd3:
  hosts: fixture-etcd:2379
bootstrap:
  dcs:
    ttl: 20
    loop_wait: 2
    retry_timeout: 3
    # One member only. Never substitute this for production synchronous policy.
    synchronous_mode: false
    postgresql:
      parameters:
        wal_level: replica
        max_wal_senders: 5
        max_replication_slots: 5
        wal_log_hints: 'on'
  initdb: [encoding: UTF8, data-checksums]
  pg_hba: ['local all all trust', 'host all all 0.0.0.0/0 scram-sha-256', 'host replication replicator 0.0.0.0/0 scram-sha-256']
postgresql:
  listen: 0.0.0.0:5432
  connect_address: fixture-a:5432
  data_dir: /var/lib/postgresql/data/pgdata
  authentication:
    superuser: {username: postgres}
    replication: {username: replicator}
tags: {nofailover: false, noloadbalance: false, clonefrom: false}
YAML
chmod 644 "$work/patroni.yml"
docker network create --internal "$network" >/dev/null
docker volume create --label hexthings.disposable=patroni-bootstrap "$volume" >/dev/null
docker volume create --label hexthings.disposable=patroni-bootstrap "$replica_volume" >/dev/null
# Reuse the already-built image to run the checksum-verified static etcd binary.
docker run -d --name "$etcd" --network "$network" --network-alias fixture-etcd \
  --mount "type=bind,source=$etcd_binary,target=/fixture-etcd,readonly" \
  --entrypoint /fixture-etcd "$image" --name fixture-etcd --data-dir /tmp/etcd-data \
  --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://fixture-etcd:2379 \
  --listen-peer-urls http://0.0.0.0:2380 --initial-advertise-peer-urls http://fixture-etcd:2380 \
  --initial-cluster fixture-etcd=http://fixture-etcd:2380 >/dev/null
start_patroni() {
  # Do not override entrypoint: this is the shipped /entrypoint.sh path.
  docker run -d --name "$name" --network "$network" --network-alias fixture-a \
    --mount "type=volume,source=$volume,target=/var/lib/postgresql/data" \
    --mount "type=bind,source=$work/patroni.yml,target=/etc/patroni/patroni.yml,readonly" \
    --env PATRONI_SUPERUSER_PASSWORD=synthetic-fixture-only \
    --env PATRONI_REPLICATION_PASSWORD=synthetic-replication-only "$image" >/dev/null
}
wait_primary() {
  for ((attempt=0; attempt<90; attempt++)); do
    if docker exec "$name" /opt/patroni/bin/python -c 'import json,urllib.request; r=urllib.request.urlopen("http://127.0.0.1:8008/primary",timeout=1); d=json.load(r); assert r.status==200 and d["state"]=="running" and d["role"] in ("primary","master")' 2>/dev/null; then
      return
    fi
    test "$(docker inspect --format '{{.State.Running}}' "$name")" = true
    sleep 1
  done
  echo 'single-node Patroni did not become primary before deadline' >&2
  return 1
}
sql() {
  docker exec "$name" psql -X -A -t -v ON_ERROR_STOP=1 -U postgres -d postgres -c "$1"
}
start_patroni
wait_primary
test "$(docker exec "$name" id -un)" = postgres
test "$(sql 'SHOW server_version_num')" -ge 160000
test "$(sql 'SELECT pg_is_in_recovery()')" = f
system_id=$(sql 'SELECT system_identifier FROM pg_control_system()')
[[ $system_id =~ ^[0-9]+$ ]]
sql "CREATE TABLE patroni_image_checkpoint (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO patroni_image_checkpoint VALUES (1, 'synthetic-entrypoint-restart')" >/dev/null
printf 'Patroni actual entrypoint bootstrap and primary SQL passed\n'
docker stop --time 30 "$name" >/dev/null
docker rm -v "$name" >/dev/null
start_patroni
wait_primary
test "$(sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 1
test "$(sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 1')" = synthetic-entrypoint-restart
test "$(sql 'SELECT pg_is_in_recovery()')" = f
printf 'Patroni actual entrypoint restart retained system identifier and committed row\n'
# The second member has its own empty volume. Never reuse primary PGDATA.
sed -e 's/name: fixture-a/name: fixture-b/' \
  -e 's/fixture-a:/fixture-b:/g' \
  -e 's/nofailover: false/nofailover: true/' "$work/patroni.yml" > "$work/replica.yml"
chmod 644 "$work/replica.yml"
start_replica() {
  docker run -d --name "$replica" --network "$network" --network-alias fixture-b \
    --mount "type=volume,source=$replica_volume,target=/var/lib/postgresql/data" \
    --mount "type=bind,source=$work/replica.yml,target=/etc/patroni/patroni.yml,readonly" \
    --env PATRONI_SUPERUSER_PASSWORD=synthetic-fixture-only \
    --env PATRONI_REPLICATION_PASSWORD=synthetic-replication-only "$image" >/dev/null
}
wait_replica() {
  for ((attempt=0; attempt<90; attempt++)); do
    if docker exec "$replica" /opt/patroni/bin/python -c 'import json,urllib.request; r=urllib.request.urlopen("http://127.0.0.1:8008/replica",timeout=1); d=json.load(r); assert r.status==200 and d["state"]=="running" and d["role"]=="replica"' 2>/dev/null; then
      return
    fi
    test "$(docker inspect --format '{{.State.Running}}' "$replica")" = true
    sleep 1
  done
  echo 'Patroni replica did not become ready before deadline' >&2
  return 1
}
replica_sql() {
  docker exec "$replica" psql -X -A -t -v ON_ERROR_STOP=1 -U postgres -d postgres -c "$1"
}
start_replica
wait_replica
test "$(docker exec "$replica" id -un)" = postgres
test "$(replica_sql 'SHOW server_version_num')" -ge 160000
test "$(replica_sql 'SELECT pg_is_in_recovery()')" = t
test "$(replica_sql 'SHOW transaction_read_only')" = on
test "$(replica_sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(replica_sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 1')" = synthetic-entrypoint-restart
printf 'Patroni separate-volume replica joined with same system identifier and base row\n'
# A row written after replica readiness cannot be explained by initial base backup.
sql "INSERT INTO patroni_image_checkpoint VALUES (2, 'synthetic-after-replica-join')" >/dev/null
target_lsn=$(sql 'SELECT pg_current_wal_lsn()')
[[ $target_lsn =~ ^[0-9A-F]+/[0-9A-F]+$ ]]
replayed=false
for ((attempt=0; attempt<60; attempt++)); do
  if [[ $(replica_sql "SELECT pg_last_wal_replay_lsn() >= '$target_lsn'::pg_lsn") == t ]]; then
    replayed=true
    break
  fi
  sleep 1
done
test "$replayed" = true
test "$(replica_sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 2
test "$(replica_sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 2')" = synthetic-after-replica-join
# Assert the actual database refusal, not just REST role or a read-only setting.
if replica_sql "INSERT INTO patroni_image_checkpoint VALUES (3, 'must-not-write')" > "$work/write-rejection.log" 2>&1; then
  echo 'standby accepted an INSERT' >&2
  exit 1
fi
grep -F 'cannot execute INSERT in a read-only transaction' "$work/write-rejection.log" >/dev/null
test "$(sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 2
test "$(replica_sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 2
test "$(sql 'SELECT pg_is_in_recovery()')" = f
test "$(replica_sql 'SELECT pg_is_in_recovery()')" = t
printf 'Patroni post-join WAL replay and standby INSERT rejection passed\n'
# Replace only the standby. The primary remains online and must not change roles.
docker stop --time 30 "$replica" >/dev/null
docker rm -v "$replica" >/dev/null
sql "INSERT INTO patroni_image_checkpoint VALUES (3, 'synthetic-while-replica-offline')" >/dev/null
offline_lsn=$(sql 'SELECT pg_current_wal_lsn()')
[[ $offline_lsn =~ ^[0-9A-F]+/[0-9A-F]+$ ]]
start_replica
wait_replica
test "$(replica_sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(replica_sql 'SELECT pg_is_in_recovery()')" = t
test "$(replica_sql 'SHOW transaction_read_only')" = on
caught_up=false
for ((attempt=0; attempt<60; attempt++)); do
  if [[ $(replica_sql "SELECT pg_last_wal_replay_lsn() >= '$offline_lsn'::pg_lsn") == t ]]; then
    caught_up=true
    break
  fi
  sleep 1
done
test "$caught_up" = true
test "$(replica_sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 3
test "$(replica_sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 1')" = synthetic-entrypoint-restart
test "$(replica_sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 2')" = synthetic-after-replica-join
test "$(replica_sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 3')" = synthetic-while-replica-offline
test "$(sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 3
test "$(sql 'SELECT pg_is_in_recovery()')" = f
test "$(sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
printf 'Patroni same-volume replica replacement replayed offline primary write and retained all three rows\n'
# Restart the same DCS container so its existing data directory is retained.
# This intentionally does not simulate a prolonged outage or a lost quorum.
docker restart --time 10 "$etcd" >/dev/null
dcs_healthy=false
for ((attempt=0; attempt<30; attempt++)); do
  if docker exec "$name" /opt/patroni/bin/python -c 'import json,urllib.request; r=urllib.request.urlopen("http://fixture-etcd:2379/health",timeout=1); d=json.load(r); assert r.status==200 and d["health"] in (True,"true")' 2>/dev/null; then
    dcs_healthy=true
    break
  fi
  test "$(docker inspect --format '{{.State.Running}}' "$etcd")" = true
  sleep 1
done
test "$dcs_healthy" = true
wait_primary
wait_replica
test "$(sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(replica_sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(sql 'SELECT pg_is_in_recovery()')" = f
test "$(replica_sql 'SELECT pg_is_in_recovery()')" = t
sql "INSERT INTO patroni_image_checkpoint VALUES (4, 'synthetic-after-dcs-restart')" >/dev/null
dcs_lsn=$(sql 'SELECT pg_current_wal_lsn()')
[[ $dcs_lsn =~ ^[0-9A-F]+/[0-9A-F]+$ ]]
dcs_replayed=false
for ((attempt=0; attempt<60; attempt++)); do
  if [[ $(replica_sql "SELECT pg_last_wal_replay_lsn() >= '$dcs_lsn'::pg_lsn") == t ]]; then
    dcs_replayed=true
    break
  fi
  sleep 1
done
test "$dcs_replayed" = true
test "$(sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 4
test "$(replica_sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 4
test "$(replica_sql 'SELECT value FROM patroni_image_checkpoint WHERE id = 4')" = synthetic-after-dcs-restart
printf 'Patroni short same-container etcd restart recovered health and replayed fourth primary row\n'
# The sole fixture DCS is unavailable. Failsafe is not enabled and the
# standby is nofailover. Require the former primary to become read-only.
docker stop --time 10 "$etcd" >/dev/null
test "$(docker inspect --format '{{.State.Running}}' "$etcd")" = false
demoted=false
for ((attempt=0; attempt<60; attempt++)); do
  if [[ $(sql 'SELECT pg_is_in_recovery()' 2>/dev/null || true) == t ]] && \
     [[ $(sql 'SHOW transaction_read_only' 2>/dev/null || true) == on ]]; then
    demoted=true
    break
  fi
  test "$(docker inspect --format '{{.State.Running}}' "$name")" = true
  sleep 1
done
test "$demoted" = true
# Three independent rounds, separated by waits, not one transient snapshot.
for round in 1 2 3; do
  test "$(docker inspect --format '{{.State.Running}}' "$etcd")" = false
  test "$(sql 'SELECT pg_is_in_recovery()')" = t
  test "$(sql 'SHOW transaction_read_only')" = on
  test "$(replica_sql 'SELECT pg_is_in_recovery()')" = t
  test "$(replica_sql 'SHOW transaction_read_only')" = on
  # HTTP503 must be from the live REST API, not a connection/parse error.
  docker exec "$name" /opt/patroni/bin/python -c 'import urllib.request,urllib.error
try:
    urllib.request.urlopen("http://127.0.0.1:8008/primary",timeout=2)
except urllib.error.HTTPError as e:
    assert e.code==503
else:
    raise AssertionError("former primary remains eligible")'
  if sql "INSERT INTO patroni_image_checkpoint VALUES (5, 'must-not-write-dcs-down')" > "$work/outage-write-rejection.log" 2>&1; then
    echo 'former primary accepted INSERT with DCS stopped' >&2
    exit 1
  fi
  grep -F 'cannot execute INSERT in a read-only transaction' "$work/outage-write-rejection.log" >/dev/null
  test "$(sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 4
  test "$(replica_sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 4
  if replica_sql "INSERT INTO patroni_image_checkpoint VALUES (5, 'must-not-write-standby-dcs-down')" > "$work/standby-outage-rejection.log" 2>&1; then
    echo 'standby accepted INSERT with DCS stopped' >&2
    exit 1
  fi
  grep -F 'cannot execute INSERT in a read-only transaction' "$work/standby-outage-rejection.log" >/dev/null
  printf 'Patroni stopped-DCS read-only/write-refusal round %s passed for both members\n' "$round"
  if [[ $round != 3 ]]; then sleep 3; fi
done
printf 'Patroni sole-DCS outage retained both read-only roles through three rounds\n'
docker start "$etcd" >/dev/null
wait_primary
wait_replica
test "$(sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(replica_sql 'SELECT system_identifier FROM pg_control_system()')" = "$system_id"
test "$(sql 'SELECT pg_is_in_recovery()')" = f
sql "INSERT INTO patroni_image_checkpoint VALUES (5, 'synthetic-after-dcs-outage')" >/dev/null
recovery_lsn=$(sql 'SELECT pg_current_wal_lsn()')
[[ $recovery_lsn =~ ^[0-9A-F]+/[0-9A-F]+$ ]]
outage_replayed=false
for ((attempt=0; attempt<60; attempt++)); do
  if [[ $(replica_sql "SELECT pg_last_wal_replay_lsn() >= '$recovery_lsn'::pg_lsn") == t ]]; then
    outage_replayed=true
    break
  fi
  sleep 1
done
test "$outage_replayed" = true
test "$(sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 5
test "$(replica_sql 'SELECT count(*) FROM patroni_image_checkpoint')" = 5
# Check every expected value on both nodes, not just total row count.
expected_values=synthetic-entrypoint-restart,synthetic-after-replica-join,synthetic-while-replica-offline,synthetic-after-dcs-restart,synthetic-after-dcs-outage
value_query="SELECT string_agg(value, ',' ORDER BY id) FROM patroni_image_checkpoint"
test "$(sql "$value_query")" = "$expected_values"
test "$(replica_sql "$value_query")" = "$expected_values"
test "$(replica_sql 'SELECT pg_is_in_recovery()')" = t
printf 'Patroni sole-DCS restore recovered original primary and replayed fifth row\n'
docker stop --time 30 "$replica" >/dev/null
docker stop --time 30 "$name" >/dev/null
printf 'Patroni image restart and repeated sole-DCS fail-closed recovery checkpoint passed\n'
