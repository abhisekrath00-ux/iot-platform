#!/usr/bin/env bash
# HexThings terminal app: one entry point for installing, running and looking after a deployment.
# The existing installers are not replaced: "single" hands over to install.sh unchanged (its own animated
# wordmark and guided steps stay exactly as they were).
#
#   bash scripts/setup.sh                  animated wordmark + interactive menu (needs a terminal)
#   bash scripts/setup.sh single [args]    install one machine (= scripts/install.sh [args])
#   bash scripts/setup.sh multi  [args]    3-node HA generator (= scripts/install-ha.sh [args]); no args: guided prompts in the menu
#   bash scripts/setup.sh status           services and their state
#   bash scripts/setup.sh health           live probes: API, web, MQTT, Postgres, with latency
#   bash scripts/setup.sh logs [service]   follow logs (Ctrl-C stops following)
#   bash scripts/setup.sh backup | verify FILE | restore FILE | upgrade
#   bash scripts/setup.sh cluster [host]   Patroni view: leader, replicas, lag (HA installs; host defaults to $HEXTHINGS_NODE or localhost)
# Menu keys: up/down or j/k, Enter, number keys, q. Works without colour (NO_COLOR), on narrow terminals,
# and without a terminal (subcommands only; the menu refuses with a usage line).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"; root="$(cd "$here/.." && pwd)"
cd "$root"
usage() { sed -n 2,17p "$0"; }
if [ "${1:-}" = -h ] || [ "${1:-}" = --help ]; then usage; exit 0; fi
LOG=/dev/null
# shellcheck source=hx-ui.sh
. "$here/hx-ui.sh"
cols() { local c=${COLUMNS:-$(tput cols 2>/dev/null || echo 80)}; [ "$c" -ge 20 ] 2>/dev/null || c=80; echo "$c"; }
clip() { local c; c=$(( $(cols) - 6 )); printf '%s' "${1:0:$c}"; }
dot_ok() { if [ "$COLOR" = 1 ]; then printf '%s●%s' "$G" "$N"; else printf 'OK '; fi; }
dot_bad() { if [ "$COLOR" = 1 ]; then printf '%s●%s' "$R" "$N"; else printf 'XX '; fi; }
dot_warn() { if [ "$COLOR" = 1 ]; then printf '%s●%s' "$Y" "$N"; else printf '?? '; fi; }
need_docker() { command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 || { echo "  Docker is not available here (not installed, not running, or not allowed for this user)."; return 1; }; }
compose() { docker compose "$@"; }

probe_http() { # probe_http name url -> prints a row, returns 0 when healthy
  local name=$1 url=$2 out code t
  if ! command -v curl >/dev/null 2>&1; then printf '  %s %-10s no curl to probe with\n' "$(dot_warn)" "$name"; return 2; fi
  out=$(curl -s -o /dev/null --max-time 3 -w '%{http_code} %{time_total}' "$url" 2>/dev/null) || out="000 0"
  code=${out%% *}; t=${out##* }
  if [ "$code" -ge 200 ] 2>/dev/null && [ "$code" -lt 400 ]; then printf '  %s %-10s healthy   %s  %4.0f ms\n' "$(dot_ok)" "$name" "$url" "$(awk -v x="$t" 'BEGIN{print x*1000}')"; return 0; fi
  printf '  %s %-10s DOWN      %s  (http %s)\n' "$(dot_bad)" "$name" "$url" "$code"; return 1
}
probe_tcp() { # probe_tcp name host port
  if (exec 3<>"/dev/tcp/$2/$3") 2>/dev/null; then printf '  %s %-10s healthy   %s:%s\n' "$(dot_ok)" "$1" "$2" "$3"; return 0; fi
  printf '  %s %-10s DOWN      %s:%s\n' "$(dot_bad)" "$1" "$2" "$3"; return 1
}
cmd_health() {
  local bad=0
  echo ""; printf '  %sHealth%s\n\n' "$B" "$N"
  probe_http api "http://${HEXTHINGS_HOST:-localhost}:8000/healthz" || bad=1
  probe_http web "http://${HEXTHINGS_HOST:-localhost}:8080/" || bad=1
  probe_tcp mqtt "${HEXTHINGS_HOST:-localhost}" "${HEXTHINGS_MQTT_PORT:-1883}" || bad=1
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    if compose exec -T postgres pg_isready -q 2>/dev/null; then printf '  %s %-10s healthy   pg_isready\n' "$(dot_ok)" postgres
    else printf '  %s %-10s not answering via compose (HA installs run Postgres under Patroni: use "cluster")\n' "$(dot_warn)" postgres; fi
  else printf '  %s %-10s skipped (Docker not available)\n' "$(dot_warn)" postgres; fi
  echo ""; [ "$bad" = 0 ] && echo "  All probed services answer." || echo "  Something is down. Try: status, logs api."
  return "$bad"
}
cmd_status() {
  need_docker || return 1
  local rows
  rows=$(compose ps --format '{{.Service}}|{{.State}}|{{.Health}}' 2>/dev/null) || { echo "  No compose project found in $root. Install first (single node)."; return 1; }
  [ -n "$rows" ] || { echo "  Nothing is running. Start with: docker compose up -d"; return 0; }
  echo ""; printf '  %sServices%s\n\n' "$B" "$N"
  local s st h up=0 total=0
  while IFS='|' read -r s st h; do
    [ -n "$s" ] || continue; total=$((total+1))
    if [ "$st" = running ] && { [ -z "$h" ] || [ "$h" = healthy ]; }; then up=$((up+1)); printf '  %s %-14s %s%s\n' "$(dot_ok)" "$s" "$st" "${h:+ ($h)}"
    elif [ "$st" = running ]; then printf '  %s %-14s %s (%s)\n' "$(dot_warn)" "$s" "$st" "$h"
    else printf '  %s %-14s %s\n' "$(dot_bad)" "$s" "$st"; fi
  done <<<"$rows"
  echo ""; printf '  %d of %d services up  %s\n' "$up" "$total" "$(bar "$up" "$total")"
}
bar() { local n=$1 t=$2 w=20 i out=""; [ "$t" -gt 0 ] || t=1; for ((i=0;i<w;i++)); do if [ $((i*t)) -lt $((n*w)) ]; then out+="$BAR_ON"; else out+="$BAR_OFF"; fi; done; printf '%s' "$out"; }
cmd_logs() {
  need_docker || return 1
  echo "  Following logs${1:+ for $1}. Press Ctrl-C to stop."
  trap ':' INT   # Ctrl-C ends the log follower below, not this app
  ( trap - INT; compose logs --tail 100 -f ${1:+"$1"} ) || true
  trap 'cleanup 2>/dev/null; echo; exit 130' INT TERM
}
cmd_cluster() {
  local host=${1:-${HEXTHINGS_NODE:-localhost}}
  command -v curl >/dev/null 2>&1 || { echo "  curl is needed for the cluster view."; return 1; }
  local j; j=$(curl -s --max-time 4 "http://$host:8008/cluster") || j=""
  [ -n "$j" ] || { echo "  No answer from Patroni at $host:8008. This view needs the multi-node install (see docs/ha-multi-node.md) and a node address."; return 1; }
  echo ""; printf '  %sCluster%s  (via %s)\n\n' "$B" "$N" "$host"
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$j" | HX_COLOR=$COLOR python3 -c '
import sys,json
try: d=json.load(sys.stdin)
except Exception: print("  Patroni answered but not with JSON; raw reply follows."); sys.exit(1)
c=lambda n,t: ("\033[%sm%s\033[0m"%(n,t)) if __import__("os").environ.get("HX_COLOR")=="1" else t
for m in d.get("members",[]):
    role=m.get("role","?"); st=m.get("state","?")
    good = st in ("running","streaming")
    dot = c("32","●") if good else c("31","●")
    tag = {"leader":"LEADER ","sync_standby":"SYNC   ","replica":"replica"}.get(role,role)
    lag = m.get("lag","-")
    print("  %s %-12s %s %-10s lag %s" % (dot,m.get("name","?"),tag,st,lag))
print()
print("  %d member(s)." % len(d.get("members",[])))
' || printf '%s\n' "$j"
  else printf '%s\n' "$j"; fi
}
run_script() { local s=$1; shift; [ -f "$here/$s" ] || { echo "  $s is missing from this checkout."; return 1; }; bash "$here/$s" "$@"; }
cmd_restore() {
  local f=${1:-}
  [ -n "$f" ] && [ -f "$f" ] || { echo "  Give an existing backup file: setup.sh restore backups/<file>.dump.gz"; return 2; }
  echo "  Verifying the backup first (integrity and dry run)..."
  run_script restore.sh --verify "$f" || { echo "  Verification failed. Nothing was changed."; return 1; }
  echo "  A full restore REPLACES the current database. API and ingest are stopped during it."
  local a; read -r -p "  Type RESTORE to continue: " a || a=""
  [ "$a" = RESTORE ] || { echo "  Cancelled. Nothing was changed."; return 0; }
  need_docker || return 1
  compose stop api ingest
  run_script restore.sh "$f"; local rc=$?
  compose start api ingest
  [ "$rc" = 0 ] && echo "  Restore finished; api and ingest started." || echo "  Restore FAILED (exit $rc); api and ingest were started again. Check the output above before using the platform."
  return "$rc"
}
guided_multi() {
  echo ""; echo "  Multi node needs exactly 3 machines and one free address (the virtual IP) on their network."
  local n1 n2 n3 vip ifc out
  read -r -p "  Node 1 as name=ip (e.g. n1=10.0.0.11): " n1 || return 1
  read -r -p "  Node 2 as name=ip: " n2 || return 1
  read -r -p "  Node 3 as name=ip: " n3 || return 1
  read -r -p "  Virtual IP: " vip || return 1
  read -r -p "  Network interface [eth0]: " ifc || true; ifc=${ifc:-eth0}
  read -r -p "  Output folder [./ha-out]: " out || true; out=${out:-./ha-out}
  run_script install-ha.sh plan --nodes "$n1,$n2,$n3" --vip "$vip" || return 1
  local a; read -r -p "  Generate the per-node folders now? [y/N]: " a || a=""
  case "$a" in y|Y) run_script install-ha.sh render --nodes "$n1,$n2,$n3" --vip "$vip" --iface "$ifc" --out "$out" ;; *) echo "  Nothing generated." ;; esac
}
dispatch() {
  case "$1" in
    single) shift; exec bash "$here/install.sh" "$@" ;;
    multi) shift; if [ $# -eq 0 ]; then if [ -t 0 ]; then guided_multi; else echo "multi node: bash scripts/setup.sh multi plan|render|check|drill --nodes n1=IP,n2=IP,n3=IP --vip IP"; echo "details: docs/ha-multi-node.md"; fi; else bash "$here/install-ha.sh" "$@"; fi ;;
    status) cmd_status ;; health) cmd_health ;; logs) shift; cmd_logs "${1:-}" ;;
    backup) run_script backup.sh ;; verify) shift; run_script restore.sh --verify "${1:?usage: setup.sh verify FILE}" ;;
    restore) shift; cmd_restore "${1:-}" ;; upgrade) run_script upgrade.sh ;;
    cluster) shift; cmd_cluster "${1:-}" ;;
    *) echo "unknown command: $1" >&2; usage >&2; return 2 ;;
  esac
}

if [ $# -gt 0 ]; then dispatch "$@"; exit $?; fi
[ -t 0 ] && [ -t 1 ] || { echo "usage: setup.sh single|multi|status|health|logs|backup|verify|restore|upgrade|cluster [args]  (the interactive menu needs a terminal)" >&2; exit 2; }

# ---- interactive menu -------------------------------------------------------------------------
labels=("Install single node" "Install multi node (3-node HA)" "Status" "Health check" "Follow logs" "Backup now" "Restore a backup" "Upgrade" "Cluster view" "Quit")
hints=("one machine, Docker Compose (the guided installer)" "generate per-node setup with automatic database failover" "services and their state" "live probes with latency" "pick a service, Ctrl-C to stop" "Postgres dump to ./backups" "verify, confirm, then restore" "backup, update, health check, auto-rollback" "leader, replicas and lag (HA installs)" "")
keys=(single multi status health logs backup restore upgrade cluster quit)
sel=0; n=${#labels[@]}
cleanup() { [ "$COLOR" = 1 ] && printf '\033[?25h'; }
trap 'cleanup; echo; exit 130' INT TERM
trap cleanup EXIT
draw() {
  local i line
  for ((i=0;i<n;i++)); do
    line="$(printf '%d. %-32s %s' $(( (i+1)%10 )) "${labels[$i]}" "${hints[$i]}")"; line=$(clip "$line")
    if [ "$i" = "$sel" ]; then
      if [ "$COLOR" = 1 ]; then printf '  %s%s▸%s %s%s%s\033[K\n' "$C1" "$B" "$N" "$B" "$line" "$N"; else printf '  > %s\n' "$line"; fi
    else
      if [ "$COLOR" = 1 ]; then printf '    %s%s%s\033[K\n' "$D" "$line" "$N"; else printf '    %s\n' "$line"; fi
    fi
  done
}
header() { [ "$COLOR" = 1 ] && printf '\033[2J\033[H'; wordmark "terminal"; footer; echo ""; printf '  %s%s%s\n\n' "$D" "$(clip 'Up/down or j/k, Enter, number keys, q to quit.')" "$N"; }
pause() { echo ""; read -r -p "  Press Enter to return to the menu " _ || true; }
while :; do
  header
  if [ "$COLOR" = 1 ]; then
    printf '\033[?25l'; draw
    while :; do
      IFS= read -rsn1 k || exit 0
      case "$k" in
        $'\033') IFS= read -rsn2 -t 0.2 k2 || true; case "${k2:-}" in '[A') sel=$(( (sel+n-1)%n )) ;; '[B') sel=$(( (sel+1)%n )) ;; esac ;;
        k) sel=$(( (sel+n-1)%n )) ;; j) sel=$(( (sel+1)%n )) ;;
        [1-9]) sel=$((k-1)); break ;; 0) sel=$((n-1)); break ;;
        q|Q) sel=$((n-1)); break ;; '') break ;;
        *) ;;   # any other key: ignored, no crash
      esac
      printf '\033[%dA' "$n"; draw
    done
    printf '\033[?25h'; echo ""
  else
    draw; read -r -p "  Choose 1-$n (q to quit): " c || exit 0
    case "$c" in q|Q) c=$n ;; esac
    if [[ "$c" =~ ^[0-9]+$ ]] && [ "$c" -ge 1 ] && [ "$c" -le "$n" ]; then sel=$((c-1)); else echo "  '$c' is not a choice."; sleep 1; continue; fi
  fi
  k=${keys[$sel]}
  case "$k" in
    quit) echo "  Bye."; exit 0 ;;
    single) dispatch single; exit $? ;;
    multi) guided_multi; pause ;;
    status) cmd_status; pause ;;
    health) cmd_health; pause ;;
    logs) read -r -p "  Service name (blank = all): " s || s=""; cmd_logs "$s"; pause ;;
    backup) run_script backup.sh; pause ;;
    restore) read -r -p "  Backup file path: " f || f=""; cmd_restore "$f"; pause ;;
    upgrade) run_script upgrade.sh; pause ;;
    cluster) read -r -p "  A node address [localhost]: " h || h=""; cmd_cluster "${h:-localhost}"; pause ;;
  esac
done
