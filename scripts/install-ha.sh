#!/usr/bin/env bash
# HexThings multi-node (high availability) installer. Generates one configuration directory per node. Failover is an unverified design target.
#
#   scripts/install-ha.sh plan   --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10
#   scripts/install-ha.sh render --nodes ... --vip ... [--iface eth0] [--out ./ha-out] [--sync-strict]
#   scripts/install-ha.sh check  --nodes ... --vip ...      (run on a node: tools, ports, clock)
#   scripts/install-ha.sh drill  --vip 10.0.0.10            (prints and runs the failover drill steps)
#
# Optional --etcd-mtls secures etcd client/peer traffic with operator-supplied certificates.
# Optional --shared-cache-mtls wires one operator-managed rediss endpoint for all APIs.
# It requires SHARED_CACHE_AUTHORITY in cache.env and cache-tls/{ca.crt,client.crt,client.key}
# on each node. It does not deploy or fail over Valkey. See docs/ha-multi-node.md.
# Needs exactly 3 nodes (etcd and Patroni need a majority; 2 nodes cannot tell a dead peer from a broken
# link, so they cannot fail over safely). It renders files only; it does not log in to your servers.
# Copy ha-out/<node> to its node (inside a checkout of this repo, or set HEXTHINGS_SRC) and run
# `docker compose up -d --build` there. STATUS: the generator is tested (scripts/test-install-ha.sh);
# the rendered stack has never been started on real machines. See docs/ha-multi-node.md.
set -euo pipefail
cmd="${1:-}"; [ $# -gt 0 ] && shift || true
NODES=""; VIP=""; IFACE="eth0"; OUT="./ha-out"; STRICT=0; SHARED_CACHE=0; ETCD_MTLS=0
while [ $# -gt 0 ]; do
  case "$1" in
    --nodes) NODES="${2:?}"; shift ;;
    --vip) VIP="${2:?}"; shift ;;
    --iface) IFACE="${2:?}"; shift ;;
    --out) OUT="${2:?}"; shift ;;
    --sync-strict) STRICT=1 ;;
    --shared-cache-mtls) SHARED_CACHE=1 ;;
    --etcd-mtls) ETCD_MTLS=1 ;;
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
  every node: etcd member, Patroni+Postgres, HAProxy, keepalived, api, ingest, mcp, web, mosquitto
  Postgres: 1 leader, 2 replicas; automatic promotion; synchronous replication$( [ "$STRICT" = 1 ] && echo " STRICT (writes stop rather than lose data)" || echo " (falls back to async if every replica is down)")
  Clients use: web http://$VIP:8080, API http://$VIP:8000, MQTT $VIP:1883, Postgres $VIP:5000 (always the leader)
  Design target (UNVERIFIED): tolerate loss of any ONE node; requires real multi-host drills.
  Two-node loss is intended to stop database writes without quorum; not a measured guarantee.
  OAuth state: shared Postgres. Response cache: $( [ "$SHARED_CACHE" = 0 ] && echo "OFF (no node-local stale response entries)" || echo "external shared cache, operator-managed" ).
P
  [ "$ETCD_MTLS" = 0 ] || echo "  etcd: explicit member/client mTLS; operator-supplied CA and separate member/Patroni certs required. Other transports unchanged."
  [ "$SHARED_CACHE" = 0 ] || echo "  Shared cache: external rediss endpoint with required mTLS files; NOT deployed/replicated by this plan."
  for k in 0 1 2; do echo "  ${NAMES[$k]}  ${IPS[$k]}  keepalived priority $((100 - 10*k))"; done
}

render() {
  parse_nodes
  [ ! -e "$OUT" ] || [ -z "$(ls -A "$OUT" 2>/dev/null)" ] || die "$OUT is not empty; refusing to overwrite (move it or pick --out)"
  mkdir -p "$OUT"; chmod 700 "$OUT"
  PGSU=$(secret); PGREP=$(secret); PGAPP=$(secret); JWT=$(secret); SECKEY=$( (command -v openssl >/dev/null && openssl rand -base64 32) || head -c 32 /dev/urandom | base64 ); KA=$(secret | head -c 8)
  RESTPW=$(secret)
  ETCD_SCHEME=http; [ "$ETCD_MTLS" = 0 ] || ETCD_SCHEME=https
  CLUSTER=""; ETCDHOSTS=""; PGSERVERS=""; HAP_PG=""; HAP_WEB=""; HAP_API=""; HAP_MQ=""
  for k in 0 1 2; do
    CLUSTER+="${NAMES[$k]}=$ETCD_SCHEME://${IPS[$k]}:2380,"
    ETCDHOSTS+="${IPS[$k]}:2379,"
    HAP_PG+="    server ${NAMES[$k]} ${IPS[$k]}:5432 maxconn 200 check port 8008"$'\n'
    HAP_WEB+="    server ${NAMES[$k]} ${IPS[$k]}:8081 check"$'\n'
    HAP_API+="    server ${NAMES[$k]} ${IPS[$k]}:8001 check"$'\n'
    bk=""; [ "$k" -gt 0 ] && bk=" backup"
    HAP_MQ+="    server ${NAMES[$k]} ${IPS[$k]}:1884 check${bk}"$'\n'
  done
  CLUSTER="${CLUSTER%,}"; ETCDHOSTS="${ETCDHOSTS%,}"
  CACHE_ENV="      CACHE_BACKEND: 'off'"; CACHE_MOUNTS=""; CACHE_ENVFILE=""
  if [ "$SHARED_CACHE" = 1 ]; then
    CACHE_ENVFILE='# Required on every node: URL-encoded user:password@host:port/database (NO scheme).
# Keep the same authority/database on all nodes. TLS files belong in ./cache-tls.
SHARED_CACHE_AUTHORITY='
    CACHE_ENV='      REDIS_URL: rediss://${SHARED_CACHE_AUTHORITY:?Set SHARED_CACHE_AUTHORITY for the external shared TLS cache}
      CACHE_BACKEND: redis
      REDIS_TLS_CA_FILE: /run/cache-tls/ca.crt
      REDIS_TLS_CERT_FILE: /run/cache-tls/client.crt
      REDIS_TLS_KEY_FILE: /run/cache-tls/client.key'
    CACHE_MOUNTS='    volumes:
      - type: bind
        source: ./cache-tls
        target: /run/cache-tls
        read_only: true
        bind:
          create_host_path: false'
  fi
  ETCD_FLAGS=""; ETCD_MOUNT=""; PATRONI_ETCD='etcd3: {hosts: "'"$ETCDHOSTS"'"}'; PATRONI_MOUNT=""
  if [ "$ETCD_MTLS" = 1 ]; then
    ETCD_FLAGS='--client-cert-auth=true --trusted-ca-file=/run/etcd-tls/ca.crt
      --cert-file=/run/etcd-tls/member.crt --key-file=/run/etcd-tls/member.key
      --peer-client-cert-auth=true --peer-trusted-ca-file=/run/etcd-tls/ca.crt
      --peer-cert-file=/run/etcd-tls/member.crt --peer-key-file=/run/etcd-tls/member.key'
    ETCD_MOUNT='
      - type: bind
        source: ./etcd-tls
        target: /run/etcd-tls
        read_only: true
        bind:
          create_host_path: false'
    PATRONI_ETCD='etcd3:
  hosts: "'"$ETCDHOSTS"'"
  protocol: https
  cacert: /run/patroni-etcd-tls/ca.crt
  cert: /run/patroni-etcd-tls/client.crt
  key: /run/patroni-etcd-tls/client.key'
    PATRONI_MOUNT='
      - type: bind
        source: ./patroni-etcd-tls
        target: /run/patroni-etcd-tls
        read_only: true
        bind:
          create_host_path: false'
  fi
  SYNCSTRICT=false; [ "$STRICT" = 1 ] && SYNCSTRICT=true
  for k in 0 1 2; do
    n="${NAMES[$k]}"; ip="${IPS[$k]}"; ETCD_LISTEN=0.0.0.0; [ "$ETCD_MTLS" = 0 ] || ETCD_LISTEN="$ip"; d="$OUT/$n"; mkdir -p "$d"
    ( umask 077
    cat >"$d/.env" <<E
# Secrets shared by all nodes. Mode 600. Keep one copy in your password manager.
POSTGRES_SUPERUSER_PASSWORD=$PGSU
POSTGRES_REPLICATION_PASSWORD=$PGREP
POSTGRES_APP_PASSWORD=$PGAPP
PATRONI_REST_PASSWORD=$RESTPW
JWT_SIGNING_SECRET=$JWT
SECRETS_KEY=$SECKEY
VRRP_AUTH_PASS=$KA
HEXTHINGS_SRC=../..
E
    )
    cat >"$d/patroni.yml" <<E
scope: hexthings
name: $n
restapi: {listen: 0.0.0.0:8008, connect_address: $ip:8008}
$PATRONI_ETCD
bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 1048576
    synchronous_mode: true
    synchronous_node_count: 1
    synchronous_mode_strict: $SYNCSTRICT
    postgresql:
      use_pg_rewind: true
      parameters: {synchronous_commit: "on", wal_level: replica, hot_standby: "on", max_wal_senders: 10, max_replication_slots: 10, wal_log_hints: "on", max_connections: 300}
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
    option httpchk GET /readyz
    http-check expect status 200
    balance roundrobin
$HAP_WEB
listen api
    bind *:8000
    option httpchk GET /readyz
    http-check expect status 200
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
    image: gcr.io/etcd-development/etcd:v3.5.35
    network_mode: host
    restart: always
    command: >
      etcd --name $n --data-dir /etcd-data
      --listen-client-urls $ETCD_SCHEME://$ETCD_LISTEN:2379 --advertise-client-urls $ETCD_SCHEME://$ip:2379
      --listen-peer-urls $ETCD_SCHEME://$ETCD_LISTEN:2380 --initial-advertise-peer-urls $ETCD_SCHEME://$ip:2380
      --initial-cluster $CLUSTER --initial-cluster-state new --initial-cluster-token hexthings
      $ETCD_FLAGS
    volumes:
      - etcd:/etcd-data$ETCD_MOUNT
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
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./patroni.yml:/etc/patroni/patroni.yml:ro$PATRONI_MOUNT
    depends_on: [etcd]
    stop_grace_period: 60s
  haproxy:
    image: haproxy:3.2.26-alpine
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
      OAUTH_STATE_BACKEND: postgres
      # Cache disabled by default; shared-cache option adds one external TLS endpoint.
$CACHE_ENV
      INGEST_SHARED_GROUP: hexthings
$CACHE_MOUNTS
    ports: ["8001:8000"]
    depends_on: [mosquitto]
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
volumes: {etcd: {}, pgdata: {}, mqdata: {}}
E
    if [ "$ETCD_MTLS" = 1 ]; then
      mkdir -m 700 "$d/etcd-tls" "$d/patroni-etcd-tls"
      cat >"$d/etcd-tls/README.txt" <<'ETCDINFO'
Install operator-managed ca.crt, member.crt and member.key here before startup.
Member certificate must cover this node's advertised IP in its IP SAN, and support
both serverAuth and clientAuth for client-listener and peer connections. Use one
member identity per node. Private key mode 600, directory 700. Mount is read-only.
No CA, private key, certificate or user identity is generated. No insecure fallback.
Renewal/reload and revocation policy remain operator responsibilities.
ETCDINFO
      cat >"$d/patroni-etcd-tls/README.txt" <<'PATRONIINFO'
Install ca.crt, client.crt and client.key trusted by the etcd CA here.
Use a separate Patroni client identity with clientAuth, not the member private key.
The non-root Patroni container must be able to read these files. Directory 700 and
private key 600; set ownership for its runtime UID. Do not make keys world-readable.
Only Patroni mounts this directory; it does not receive the member private key.
This option authenticates TLS clients at etcd, not etcd RBAC users/roles. Patroni
uses etcd3's gRPC gateway; do not assume certificate CN maps to an etcd RBAC user.
Missing/invalid files must be fixed before starting. No identities are generated.
PATRONIINFO
    fi
    if [ "$SHARED_CACHE" = 1 ]; then
      ( umask 077; printf '%s\n' "$CACHE_ENVFILE" >"$d/cache.env" )
      mkdir -m 700 "$d/cache-tls"
      cat >"$d/cache-tls/README.txt" <<'CACHEINFO'
Before starting, install ca.crt, client.crt and client.key here. Keep private key
mode 600 and directory 700; use the same trusted endpoint and credentials/database
in SHARED_CACHE_AUTHORITY in cache.env on all nodes. No credentials/certificates are generated.
Start with: docker compose --env-file .env --env-file cache.env up -d --build
cache.env is used only for interpolation, never as a container env_file.
Compose refuses a missing directory; API refuses missing/invalid TLS files.
Do not start until the external cache's ACLs, TLS and backup/failover policy are
verified. This directory is mounted read-only only into API. No automatic renewal.
CACHEINFO
    fi
    START='docker compose up -d --build'
    [ "$SHARED_CACHE" = 0 ] || START='docker compose --env-file .env --env-file cache.env up -d --build (after cache.env and cache-tls setup)'
    echo "Node $n ($ip): copy this directory to the server, then: $START. If --etcd-mtls was selected, provision both TLS directories and verify trust/SAN/permissions first. Start all three within a few minutes; the first to win the etcd election initialises Postgres." >"$d/README.txt"
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
 1. On a node, note the leader: docker compose exec patroni /opt/patroni/bin/patronictl -c /etc/patroni/patroni.yml list
    Do not use VIP:8008: the generated proxy does not expose that port. Run the read-only HTTP checkpoint separately:
    python3 scripts/check-ha-http.py --nodes <three-name=IPv4-entries> --vip $VIP
 2. Write a marker through the VIP, e.g. create the table drill_marker and insert the current time via psql on $VIP:5000.
 3. On the leader node: docker compose stop patroni   (or power the VM off for the harder test)
 4. Observe whether a replica becomes leader; 30-45 seconds is an unmeasured target, not a deadline or guarantee. Re-run step 1.
 5. Read drill_marker through $VIP:5000: the marker must still be there. If it is missing the replica was not synchronous: record that as a failure.
 6. Start the stopped node again; it should rejoin as a replica (pg_rewind runs automatically).
 7. Record the measured failover time and any lost writes in docs/ha-multi-node.md.
D
}

case "$cmd" in
  plan) plan ;; render) render ;; check) check ;; drill) drill ;;
  *) echo "usage: install-ha.sh plan|render|check|drill (see --help)" >&2; exit 2 ;;
esac
