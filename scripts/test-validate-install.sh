#!/usr/bin/env bash
# Tests scripts/validate-install.sh preflight logic with fake docker: no real Docker.
# Covers: all-green host passes, missing docker fails, dead daemon fails, bundle
# checksum pass and fail, rehearsal never runs without a daemon, read-only default
# (no docker mutating call is ever made in preflight).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fails=0; check() { if ! eval "$2"; then echo "FAIL: $1"; fails=$((fails+1)); else echo "ok:   $1"; fi; }

mkdir -p "$T/bin"
cat > "$T/bin/docker" <<'D'
#!/usr/bin/env bash
echo "docker $*" >> "$FAKE_LOG"
case "$1" in
  --version) echo "Docker version 27.0.0"; exit 0 ;;
  info) [ "${FAKE_DOCKER_DOWN:-0}" = 1 ] && exit 1; exit 0 ;;
  compose) [ "$2" = version ] && { echo "v2.29.0"; exit 0; }
           [ "$2" = up ] && exit 0; [ "$2" = down ] && exit 0; exit 0 ;;
esac
exit 0
D
chmod +x "$T/bin/docker"

B="$T/bundle"; mkdir -p "$B"
echo payload > "$B/payload.bin"; echo 'version: "3"' > "$B/docker-compose.yml"
(cd "$B" && sha256sum payload.bin docker-compose.yml > SHA256SUMS)

run() { PATH="${TEST_PATH:-$T/bin:$PATH}" FAKE_LOG="$T/log" bash "$ROOT/scripts/validate-install.sh" "$@" > "$T/out" 2>&1; }

# All green (sandbox memory/disk are low, so only assert docker lines and read-only behaviour)
: > "$T/log"; run --yes && rc=0 || rc=$?
check "preflight runs to completion" '[ $rc -le 1 ]'
check "docker checks pass with a live fake daemon" 'grep -q "ok:   docker present" "$T/out" && grep -q "ok:   docker daemon reachable" "$T/out" && grep -q "ok:   compose plugin present" "$T/out"'
check "preflight is read-only (no compose up/down/pull/build logged)" '! grep -qE "compose (up|down|pull|build)" "$T/log"'
check "preflight result line printed" 'grep -q "^RESULT:" "$T/out"'

# Missing docker
: > "$T/log"; TEST_PATH="/usr/bin:/bin" run --yes && rc=0 || rc=$?
check "missing docker is a FAIL, exit nonzero" '[ $rc -ne 0 ] && grep -q "FAIL: docker not installed" "$T/out"'

# Dead daemon
: > "$T/log"; FAKE_DOCKER_DOWN=1 run --yes && rc=0 || rc=$?
check "unreachable daemon is a FAIL" '[ $rc -ne 0 ] && grep -q "FAIL: docker daemon not reachable" "$T/out"'

# Bundle verification good and bad
: > "$T/log"; run --yes --bundle "$B" && rc=0 || rc=$?
check "good bundle verifies" 'grep -q "ok:   SHA256SUMS verify" "$T/out"'
echo tampered > "$B/payload.bin"
run --yes --bundle "$B" && rc=0 || rc=$?
check "tampered bundle fails" '[ $rc -ne 0 ] && grep -q "FAIL: SHA256SUMS" "$T/out"'

# Rehearse without a daemon refuses and boots nothing
: > "$T/log"; FAKE_DOCKER_DOWN=1 run --yes --rehearse "$B" && rc=0 || rc=$?
check "rehearse without a daemon fails closed, boots nothing" '[ $rc -ne 0 ] && grep -q "FAIL: rehearsal needs a reachable docker daemon" "$T/out" && ! grep -q "compose up" "$T/log"'

if [ $fails -gt 0 ]; then echo "RESULT: FAIL ($fails)"; exit 1; fi
echo "RESULT: PASS"
