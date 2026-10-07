#!/usr/bin/env bash
# HexThings multi-node (high availability) installer. Generates one ready-to-run directory per node.
#
#   scripts/install-ha.sh plan   --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10
#   scripts/install-ha.sh render --nodes ... --vip ... [--iface eth0] [--out ./ha-out] [--sync-strict]
#   scripts/install-ha.sh check  --nodes ... --vip ...      (run on a node: tools, ports, clock)
#   scripts/install-ha.sh drill  --vip 10.0.0.10            (prints and runs the failover drill steps)
#
# Needs exactly 3 nodes (etcd and Patroni need a majority; 2 nodes cannot tell a dead peer from a broken
# link, so they cannot fail over safely). It renders files only; it does not log in to your servers.
# Copy ha-out/<node> to its node (inside a checkout of this repo, or set HEXTHINGS_SRC) and run
# `docker compose up -d --build` there. STATUS: the generator is tested (scripts/test-install-ha.sh);
# the rendered stack has never been started on real machines. See docs/ha-multi-node.md.
set -euo pipefail
cmd="${1:-}"; [ $# -gt 0 ] && shift || true
NODES=""; VIP=""; IFACE="eth0"; OUT="./ha-out"; STRICT=0
while [ $# -gt 0 ]; do
  case "$1" in
    --nodes) NODES="${2:?}"; shift ;;
    --vip) VIP="${2:?}"; shift ;;
    --iface) IFACE="${2:?}"; shift ;;
    --out) OUT="${2:?}"; shift ;;
    --sync-strict) STRICT=1 ;;
    -h|--help) sed -n 2,14p "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done
die() { echo "error: $*" >&2; exit 1; }
ipv4() { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && for o in ${1//./ }; do [ "$o" -le 255 ] || return 1; done; }
name_ok() { [[ "$1" =~ ^[a-z][a-z0-9-]{0,30}$ ]]; }

NAMES=(); IPS=()
parse_nodes() {
  [ -n "$NODES" ] || die "--nodes name=ip,name=ip,name=ip is required"
  IFS=',' read -r -a parts <<<"$NODES"
  [ "${#parts[@]}" -eq 3 ] || die "exactly 3 nodes are required (got ${#parts[@]}): two nodes cannot fail over safely without a third voter"
  for p in "${parts[@]}"; do
    n="${p%%=*}"; i="${p#*=}"
    [ "$n" != "$p" ] || die "bad node '$p' (use name=ip)"
    name_ok "$n" || die "bad node name '$n' (lowercase letters, digits, dash)"
    ipv4 "$i" || die "bad IPv4 address '$i' for node $n"
    for x in "${NAMES[@]:-}"; do [ "$x" != "$n" ] || die "duplicate node name $n"; done
    for x in "${IPS[@]:-}"; do [ "$x" != "$i" ] || die "duplicate node address $i"; done
    NAMES+=("$n"); IPS+=("$i")
  done
  [ -n "$VIP" ] || die "--vip (a free address on the same subnet) is required"
  ipv4 "$VIP" || die "bad VIP '$VIP'"
  for x in "${IPS[@]}"; do [ "$x" != "$VIP" ] || die "VIP must differ from node addresses"; done
}
secret() { if command -v openssl >/dev/null; then openssl rand -hex 24; else head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; fi; }

plan() {
  parse_nodes
  cat <<P
Plan (3 nodes + virtual IP $VIP):
  every node: etcd member, Patroni+Postgres, HAProxy, keepalived, api, ingest, mcp, web, mosquitto, redis
  Postgres: 1 leader, 2 replicas; automatic promotion; synchronous replication$( [ "$STRICT" = 1 ] && echo " STRICT (writes stop rather than lose data)" || echo " (falls back to async if every replica is down)")
  Clients use: web http://$VIP:8080, API http://$VIP:8000, MQTT $VIP:1883, Postgres $VIP:5000 (always the leader)
  Survives: loss of any ONE node. Loss of two nodes stops writes (no quorum) by design, to avoid split brain.
P
  for k in 0 1 2; do echo "  ${NAMES[$k]}  ${IPS[$k]}  keepalived priority $((100 - 10*k))"; done
}

render() {
  parse_nodes
  [ ! -e "$OUT" ] || [ -z "$(ls -A "$OUT" 2>/dev/null)" ] || die "$OUT is not empty; refusing to overwrite (move it or pick --out)"
  mkdir -p "$OUT"; chmod 700 "$OUT"
  PGSU=$(secret); PGREP=$(secret); PGAPP=$(secret); JWT=$(secret); SECKEY=$( (command -v openssl >/dev/null && openssl rand -base64 32) || head -c 32 /dev/urandom | base64 ); KA=$(secret | head -c 8)
  RESTPW=$(secret); REDISPW=$(secret)
  CLUSTER=""; ETCDHOSTS=""; PGSERVERS=""; HAP_PG=""; HAP_WEB=""; HAP_API=""; HAP_MQ=""
  for k in 0 1 2; do
    CLUSTER+="${NAMES[$k]}=http://${IPS[$k]}:2380,"
    ETCDHOSTS+="${IPS[$k]}:2379,"
    HAP_PG+="    server ${NAMES[$k]} ${IPS[$k]}:5432 maxconn 200 check port 8008"$'\n'
    HAP_WEB+="    server ${NAMES[$k]} ${IPS[$k]}:8081 check"$'\n'
    HAP_API+="    server ${NAMES[$k]} ${IPS[$k]}:8001 check"$'\n'
    bk=""; [ "$k" -gt 0 ] && bk=" backup"
    HAP_MQ+="    server ${NAMES[$k]} ${IPS[$k]}:1884 check${bk}"$'\n'
  done
  CLUSTER="${CLUSTER%,}"; ETCDHOSTS="${ETCDHOSTS%,}"
  SYNCSTRICT=false; [ "$STRICT" = 1 ] && SYNCSTRICT=true
  for k in 0 1 2; do
    n="${NAMES[$k]}"; ip="${IPS[$k]}"; d="$OUT/$n"; mkdir -p "$d"
    ( umask 077
    cat >"$d/.env" <<E
# Secrets shared by all nodes. Mode 600. Keep one copy in your password manager.
POSTGRES_SUPERUSER_PASSWORD=$PGSU
POSTGRES_REPLICATION_PASSWORD=$PGREP
POSTGRES_APP_PASSWORD=$PGAPP
PATRONI_REST_PASSWORD=$RESTPW
JWT_SIGNING_SECRET=$JWT
SECRETS_KEY=$SECKEY
REDIS_PASSWORD=$REDISPW
VRRP_AUTH_PASS=$KA
HEXTHINGS_SRC=../..
E
    )
    cat >"$d/patroni.yml" <<E
scope: hexthings
name: $n
restapi: {listen: 0.0.0.0:8008, connect_address: $ip:8008}
etcd3: {hosts: "$ETCDHOSTS"}
bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 1048576
    synchronous_mode: true
    synchronous_mode_strict: $SYNCSTRICT
    postgresql:
      use_pg_rewind: true
      parameters: {wal_level: replica, hot_standby: "on", max_wal_senders: 10, max_replication_slots: 10, wal_log_hints: "on", max_connections: 300}
  initdb: [encoding: UTF8, data-checksums]
  pg_hba: ["host replication replicator 0.0.0.0/0 scram-sha-256", "host all all 0.0.0.0/0 scram-sha-256"]
postgresql:
  listen: 0.0.0.0:5432
  connect_address: $ip:5432
  data_dir: /var/lib/postgresql/data/pgdata
  authentication:
    superuser: {username: postgres}
    replication: {username: replicator}
# passwords come from the environment (PATRONI_SUPERUSER_PASSWORD, PATRONI_REPLICATION_PASSWORD), never this file
tags: {nofailover: false, noloadbalance: false, clonefrom: false}
E
    cat >"$d/haproxy.cfg" <<E
global
    maxconn 4096
defaults
    mode tcp
    timeout connect 4s
    timeout client 30m
    timeout server 30m
    default-server inter 3s fall 3 rise 2 on-marked-down shutdown-sessions
# Postgres: only the node Patroni reports as primary answers 200 on /primary.
listen postgres
    bind *:5000
    option httpchk GET /primary
    http-check expect status 200
$HAP_PG
listen web
    bind *:8080
    balance roundrobin
$HAP_WEB
listen api
    bind *:8000
    balance roundrobin
$HAP_API
# MQTT: one broker serves at a time; the others are backups. Broker session files are NOT replicated.
listen mqtt
    bind *:1883
$HAP_MQ
E
    prio=$((100 - 10*k))
    ( umask 077; cat >"$d/keepalived.conf" <<E
vrrp_script chk_haproxy {
    script "/usr/bin/pgrep haproxy"
    interval 2
    fall 2
    rise 2
}
vrrp_instance HEXTHINGS {
    state BACKUP
    interface $IFACE
    virtual_router_id 51
    priority $prio
    advert_int 1
    authentication { auth_type PASS
                     auth_pass $KA }
    virtual_ipaddress { $VIP }
    track_script { chk_haproxy }
}
E
    )
    cat >"$d/docker-compose.yml" <<E
# Node $n ($ip). Run: docker compose up -d --build   (from inside a HexThings checkout, see HEXTHINGS_SRC)
services:
  etcd:
    image: quay.io/coreos/etcd:v3.5.14
    network_mode: host
    restart: always
    command: >
      etcd --name $n --data-dir /etcd-data
      --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://$ip:2379
      --listen-peer-urls http://0.0.0.0:2380 --initial-advertise-peer-urls http://$ip:2380
      --initial-cluster $CLUSTER --initial-cluster-state new --initial-cluster-token hexthings
    volumes: [etcd:/etcd-data]
  patroni:
    build: \${HEXTHINGS_SRC}/deploy/ha/patroni
    network_mode: host
    restart: always
    env_file: .env
    environment:
      PGDATA: /var/lib/postgresql/data/pgdata
      PATRONI_SUPERUSER_PASSWORD: \${POSTGRES_SUPERUSER_PASSWORD}
      PATRONI_REPLICATION_PASSWORD: \${POSTGRES_REPLICATION_PASSWORD}
      PATRONI_RESTAPI_USERNAME: patroni
      PATRONI_RESTAPI_PASSWORD: \${PATRONI_REST_PASSWORD}
    volumes: [pgdata:/var/lib/postgresql/data, ./patroni.yml:/etc/patroni/patroni.yml:ro]
    depends_on: [etcd]
    stop_grace_period: 60s
  haproxy:
    image: haproxy:2.9-alpine
    network_mode: host
    restart: always
    volumes: [./haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro]
  keepalived:
    image: osixia/keepalived:2.0.20
    network_mode: host
    restart: always
    cap_add: [NET_ADMIN, NET_BROADCAST, NET_RAW]
    env_file: .env
    volumes: [./keepalived.conf:/container/service/keepalived/assets/keepalived.conf:ro]
  mosquitto:
    image: eclipse-mosquitto:2
    restart: always
    ports: ["1884:1883"]
    volumes: ["\${HEXTHINGS_SRC}/deploy/mosquitto/mosquitto.conf:/mosquitto/config/mosquitto.conf:ro", "mqdata:/mosquitto/data"]
  redis:
    image: redis:7-alpine
    restart: always
    command: ["redis-server", "--requirepass", "\${REDIS_PASSWORD}", "--appendonly", "yes"]
    volumes: [redisdata:/data]
  api:
    build: \${HEXTHINGS_SRC}/server
    command: ["/bin/api"]
    restart: always
    env_file: .env
    extra_hosts: ["host.docker.internal:host-gateway"]
    environment:
      DATABASE_URL: postgres://postgres:\${POSTGRES_SUPERUSER_PASSWORD}@host.docker.internal:5000/postgres?sslmode=disable
      MQTT_HOST: mosquitto
      MQTT_PORT: "1883"
      MQTT_TLS: "false"
      API_PORT: "8000"
      REDIS_URL: redis://:\${REDIS_PASSWORD}@redis:6379
      CACHE_BACKEND: redis
      INGEST_SHARED_GROUP: hexthings
    ports: ["8001:8000"]
    depends_on: [redis, mosquitto]
  ingest:
    build: \${HEXTHINGS_SRC}/server
    command: ["/bin/ingest"]
    restart: always
    env_file: .env
    extra_hosts: ["host.docker.internal:host-gateway"]
    environment:
      DATABASE_URL: postgres://postgres:\${POSTGRES_SUPERUSER_PASSWORD}@host.docker.internal:5000/postgres?sslmode=disable
      MQTT_HOST: mosquitto
      MQTT_PORT: "1883"
      MQTT_TLS: "false"
      INGEST_SHARED_GROUP: hexthings
    depends_on: [mosquitto]
  mcp:
    build: \${HEXTHINGS_SRC}/server
    command: ["/bin/mcp"]
    restart: always
    env_file: .env
    extra_hosts: ["host.docker.internal:host-gateway"]
    environment:
      DATABASE_URL: postgres://postgres:\${POSTGRES_SUPERUSER_PASSWORD}@host.docker.internal:5000/postgres?sslmode=disable
    ports: ["8100:8100"]
    depends_on: [api]
  web:
    build: \${HEXTHINGS_SRC}/web
    restart: always
    ports: ["8081:80"]
    depends_on: [api]
volumes: {etcd: {}, pgdata: {}, mqdata: {}, redisdata: {}}
E
    echo "Node $n ($ip): copy this directory to the server, then: docker compose up -d --build. Start all three within a few minutes; the first to win the etcd election initialises Postgres." >"$d/README.txt"
  done
  echo "Rendered 3 node directories in $OUT (secrets in each .env, mode 600). Next: docs/ha-multi-node.md, then 'check' on each node, then the failover drill."
}

check() {
  parse_nodes; bad=0
  for t in docker; do command -v "$t" >/dev/null && echo "ok   $t present" || { echo "FAIL $t missing"; bad=1; }; done
  docker compose version >/dev/null 2>&1 && echo "ok   docker compose" || { echo "FAIL docker compose plugin missing"; bad=1; }
  if command -v timedatectl >/dev/null; then timedatectl show -p NTPSynchronized --value 2>/dev/null | grep -q yes && echo "ok   clock synchronised" || { echo "WARN clock not NTP-synchronised (etcd and Patroni leases depend on it)"; }; fi
  for k in 0 1 2; do for p in 2379 2380 5432 8008; do
    (exec 3<>/dev/tcp/"${IPS[$k]}"/"$p") 2>/dev/null && echo "ok   ${NAMES[$k]}:$p reachable" || echo "info ${NAMES[$k]}:$p not reachable yet (expected before first start)"
  done; done
  [ "$bad" = 0 ] || exit 1
}

drill() {
  [ -n "$VIP" ] || die "--vip required"
  cat <<D
Failover drill (run only in a maintenance window; this stops one node's database):
 1. Note the leader:   curl -s http://$VIP:8008/cluster   (or: docker compose exec patroni /opt/patroni/bin/patronictl -c /etc/patroni/patroni.yml list)
 2. Write a marker through the VIP, e.g. create the table drill_marker and insert the current time via psql on $VIP:5000.
 3. On the leader node: docker compose stop patroni   (or power the VM off for the harder test)
 4. Within about 30-45 seconds one replica should become leader. Re-run step 1.
 5. Read drill_marker through $VIP:5000: the marker must still be there. If it is missing the replica was not synchronous: record that as a failure.
 6. Start the stopped node again; it should rejoin as a replica (pg_rewind runs automatically).
 7. Record the measured failover time and any lost writes in docs/ha-multi-node.md.
D
}

case "$cmd" in
  plan) plan ;; render) render ;; check) check ;; drill) drill ;;
  *) echo "usage: install-ha.sh plan|render|check|drill (see --help)" >&2; exit 2 ;;
esac
