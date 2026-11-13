#!/bin/sh
# Non-root broker supervisor. Watches only the generated ACL, never Docker or
# an owner account. HUP delivery is not an authorization receipt; a fresh
# own-gateway nonce observed by ingest is still required by Finish pairing.
set -eu
config=${1:-/mosquitto/config/mosquitto.conf}
acl=${2:-/run/broker-acl/acl}
checksum() { value=$(sha256sum "$acl") || return 1;printf '%s\n' "${value%% *}"; }
[ -r "$config" ] && [ -r "$acl" ] || { echo 'Broker configuration/ACL unavailable' >&2;exit 1; }
last=$(checksum)
mosquitto -c "$config" &
broker=$!
stop() { kill -TERM "$broker" 2>/dev/null || true;wait "$broker" 2>/dev/null || true; }
trap stop EXIT
trap 'exit 0' TERM INT
while kill -0 "$broker" 2>/dev/null;do
 sleep 1
 # File removal/unreadability is a failure, not permission to keep old grants.
 if [ ! -r "$acl" ];then echo 'Broker ACL became unavailable' >&2;stop;exit 1;fi
 current=$(checksum)
 if [ "$current" != "$last" ];then
  kill -HUP "$broker" || { stop;exit 1; }
  last=$current
  echo 'Generated broker ACL changed; reload signal sent, authorization receipt still required.'
 fi
done
wait "$broker"
