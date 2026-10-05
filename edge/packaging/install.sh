#!/usr/bin/env bash
# Guided installer for the HexThings edge agent (Linux, x86_64 and arm64).
#
#   sudo ./install.sh                          asks for the server address and claim code
#   sudo ./install.sh --server URL --code CODE --serial SERIAL [--yes]
#
# It checks the machine, tests that the server answers, then runs install-linux.sh (service,
# unprivileged user, hardened unit). Works air-gapped: the binary is next to this script and
# only your own HexThings server is contacted. Plain ASCII output when not a terminal,
# NO_COLOR is set, or HEXTHINGS_ASCII=1.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
SERVER=""; CODE=""; SERIAL=""; YES=0; SKIPNET="${HEXTHINGS_SKIP_NET_CHECK:-0}"
while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:?}"; shift ;;
    --code) CODE="${2:?}"; shift ;;
    --serial) SERIAL="${2:?}"; shift ;;
    --yes|-y) YES=1 ;;
    --skip-net-check) SKIPNET=1 ;;
    --help|-h) sed -n 2,9p "$0"; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac; shift
done
C=0; U=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ] && [ -z "${HEXTHINGS_ASCII:-}" ]; then C=1; fi
case "${LC_ALL:-${LC_CTYPE:-${LANG:-}}}" in *[Uu][Tt][Ff]-8*|*[Uu][Tt][Ff]8*) [ -z "${HEXTHINGS_ASCII:-}" ] && U=1 ;; esac
c() { [ "$C" = 1 ] && printf '\033[38;5;%sm' "$1" || true; }
r() { [ "$C" = 1 ] && printf '\033[0m' || true; }
ok()   { c 41; [ "$U" = 1 ] && printf '  ✔ ' || printf '  [ok] '; r; echo "$*"; }
warn() { c 214; [ "$U" = 1 ] && printf '  ▲ ' || printf '  [!] '; r; echo "$*"; }
bad()  { c 203; [ "$U" = 1 ] && printf '  ✘ ' || printf '  [x] '; r; echo "$*"; }
step() { echo; c 39; printf '%s' "$1"; r; echo "  $2"; }
if [ "$U" = 1 ]; then
  c 45; echo '   ⬢  HexThings'; r; echo '      edge agent installer'
else
  echo '   HexThings'; echo '   edge agent installer'
fi

step "1/4" "Checking this machine"
[ "$(id -u)" = 0 ] || [ "${HEXMON_SKIP_SYSTEMD:-0}" = 1 ] || { bad "run as root: sudo ./install.sh"; exit 1; }
case "$(uname -m)" in x86_64|amd64|aarch64|arm64) ok "CPU $(uname -m)" ;; *) bad "unsupported CPU $(uname -m)"; exit 1 ;; esac
if [ "${HEXMON_SKIP_SYSTEMD:-0}" = 1 ] || command -v systemctl >/dev/null 2>&1; then ok "systemd present"; else bad "systemd not found (use the Docker option in docs/edge-install.md)"; exit 1; fi
[ -f "$here/install-linux.sh" ] || { bad "install-linux.sh not found next to this script"; exit 1; }
ok "agent binary is bundled (no internet needed)"

step "2/4" "Your HexThings server"
if [ -z "$SERVER" ] && [ "$YES" = 0 ]; then read -r -p "  Server address (https://...): " SERVER; fi
if [ -z "$CODE" ] && [ "$YES" = 0 ]; then read -r -p "  Claim code (from Devices > Add gateway): " CODE; fi
if [ -n "$SERVER" ] && [ -z "$SERIAL" ] && [ "$YES" = 0 ]; then read -r -p "  Gateway serial (printed on the claim): " SERIAL; fi
if [ -n "$SERVER" ]; then
  case "$SERVER" in http://*|https://*) ;; *) bad "server address must start with http:// or https://"; exit 1 ;; esac
  if [ "$SKIPNET" = 1 ]; then warn "server reachability check skipped"
  elif command -v curl >/dev/null 2>&1 && curl -fsS -m 8 -o /dev/null -k "${SERVER%/}/healthz" 2>/dev/null; then ok "server answers at $SERVER"
  else bad "cannot reach $SERVER (check the address, network and firewall)"; exit 1; fi
  if [ "$SKIPNET" != 1 ]; then
    d="$(curl -sI -m 8 -k "${SERVER%/}/healthz" 2>/dev/null | sed -n 's/^[Dd]ate: *//p' | head -1 | tr -d '\r')"
    if [ -n "$d" ]; then
      diff=$(( $(date +%s) - $(date -d "$d" +%s 2>/dev/null || date +%s) )); diff=${diff#-}
      if [ "$diff" -gt 300 ]; then warn "clock differs from the server by ${diff}s; fix the time or certificates and tokens may fail"; else ok "clock is in sync with the server"; fi
    fi
  fi
else warn "no server given: installing only; place /etc/hexmon/edge-agent.yaml yourself"; fi

step "3/4" "Installing the service"
args=()
if [ -n "$SERVER" ] && [ -n "$CODE" ]; then args=(-claim-api "$SERVER" -claim-code "$CODE"); [ -n "$SERIAL" ] && args+=(-claim-serial "$SERIAL"); fi
"$here/install-linux.sh" "${args[@]}" && ok "service installed" || { bad "install failed, see messages above"; exit 1; }

step "4/4" "Done"
ok "HexThings edge agent is installed"
echo "  Start:   sudo systemctl start hexmon-edge"
echo "  Logs:    journalctl -u hexmon-edge -f"
echo "  Health:  edge-agent -health"
