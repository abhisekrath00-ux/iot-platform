#!/usr/bin/env bash
# HexThings install validation for Linux hosts. READ-ONLY by default:
# it inspects the host and (optionally) verifies a downloaded bundle's
# checksums. It never installs Docker, never starts or stops services,
# never touches ports, data or service accounts.
#
# Usage:
#   validate-install.sh                      host preflight only (read-only)
#   validate-install.sh --bundle DIR         also verify DIR's SHA256SUMS
#   validate-install.sh --rehearse DIR       opt-in isolated rehearsal: boots the
#                                            bundle's stack in DIR with a cleanup
#                                            trap, checks /healthz, then tears down
#
# --rehearse starts Docker containers from the bundle. That is the only mode that
# changes anything, it asks first unless --yes is given, and it always cleans up
# (docker compose down + removal of nothing else). The rehearsal path needs real
# Docker; it is written but was NOT exercised in the build environment (no Docker
# there) - treat its first real run as the test.
#
# Windows: use scripts/validate-install.ps1 natively, or run this file under WSL.
# Native Windows is not supported by this .sh.
set -euo pipefail
BUNDLE=""; REHEARSE=0; YES=0
while [ $# -gt 0 ]; do
  case "$1" in
    --bundle) BUNDLE="$2"; shift 2 ;;
    --rehearse) REHEARSE=1; BUNDLE="$2"; shift 2 ;;
    --yes) YES=1; shift ;;
    -h|--help) sed -n 2,22p "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done
fails=0; warns=0
ok()   { echo "ok:   $1"; }
warn() { echo "WARN: $1"; warns=$((warns+1)); }
fail() { echo "FAIL: $1"; fails=$((fails+1)); }

echo "== host preflight (read-only) =="
UNAME=$(uname -srm 2>/dev/null || echo unknown)
echo "host: $UNAME"
[ "$(uname -s)" = Linux ] || warn "not Linux: for native Windows use scripts/validate-install.ps1"

if command -v docker >/dev/null 2>&1; then
  ok "docker present ($(docker --version 2>/dev/null | head -1))"
  if docker info >/dev/null 2>&1; then ok "docker daemon reachable"; else fail "docker daemon not reachable (start Docker or add the user to the docker group)"; fi
  if docker compose version >/dev/null 2>&1; then ok "compose plugin present"; else fail "docker compose plugin missing"; fi
else
  fail "docker not installed (this script never installs it; see docs/install.md)"
fi

MEM_MB=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
[ "${MEM_MB:-0}" -ge 6144 ] && ok "memory ${MEM_MB} MB (>= 6 GB)" || { [ "${MEM_MB:-0}" -ge 4096 ] && warn "memory ${MEM_MB} MB: 4-6 GB works for the CPU-only profile, 8 GB recommended" || fail "memory ${MEM_MB} MB below the 4 GB minimum"; }

DISK_MB=$(df -Pm . 2>/dev/null | awk 'NR==2{print int($4)}')
[ "${DISK_MB:-0}" -ge 20480 ] && ok "disk free ${DISK_MB} MB (>= 20 GB)" || fail "disk free ${DISK_MB:-unknown} MB below the 20 GB minimum"

for c in curl openssl sha256sum; do command -v "$c" >/dev/null 2>&1 && ok "$c present" || fail "$c missing"; done

port_free() { ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
for p in 8000 8080 1883 8883 5432; do
  if port_free $p; then ok "port $p free"; else warn "port $p already in use (the installer lets you pick others)"; fi
done

if [ -n "$BUNDLE" ]; then
  echo "== bundle verification (read-only) =="
  if [ -f "$BUNDLE/SHA256SUMS" ]; then
    if (cd "$BUNDLE" && sha256sum -c SHA256SUMS >/dev/null 2>&1); then ok "SHA256SUMS verify in $BUNDLE"; else fail "SHA256SUMS do not match in $BUNDLE"; fi
  else
    fail "no SHA256SUMS in $BUNDLE"
  fi
fi

if [ "$REHEARSE" = 1 ]; then
  echo "== isolated rehearsal (opt-in, starts and then stops containers) =="
  if [ ! -f "$BUNDLE/docker-compose.yml" ]; then
    fail "no docker-compose.yml in $BUNDLE - not an install bundle"
  elif ! docker info >/dev/null 2>&1; then
    fail "rehearsal needs a reachable docker daemon"
  else
    if [ "$YES" != 1 ]; then
      printf 'Boot the stack from %s now, check /healthz, then tear it down? [y/N] ' "$BUNDLE"
      read -r a; [ "$a" = y ] || [ "$a" = Y ] || { echo "rehearsal skipped"; exit 0; }
    fi
    ( cd "$BUNDLE"
      trap 'docker compose down -v >/dev/null 2>&1 || true' EXIT
      docker compose up -d --build
      for i in $(seq 1 120); do curl -fs -o /dev/null http://127.0.0.1:8000/healthz 2>/dev/null && break; sleep 1; done
      curl -fs -o /dev/null http://127.0.0.1:8000/healthz && echo "ok:   rehearsal stack became healthy" || { echo "FAIL: rehearsal stack never became healthy"; exit 1; }
    ) || fails=$((fails+1))
  fi
fi

echo
if [ $fails -gt 0 ]; then echo "RESULT: FAIL ($fails failures, $warns warnings)"; exit 1; fi
echo "RESULT: PASS ($warns warnings) - host preflight only; OS/service-level gates (real Docker boot, systemd, Windows service, hardware) remain open unless --rehearse ran green here"
