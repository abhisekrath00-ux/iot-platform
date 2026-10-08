#!/usr/bin/env bash
# Tests scripts/prereq.sh against fake docker/curl/sudo/systemctl programs. Nothing real is installed.
# Proves the decisions (skip what is present, ask once, install only what is missing, refuse without a yes,
# clear failures). Does NOT prove Docker's real install script works on a real distribution.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin"
cat > "$T/bin/curl" <<'C'
#!/usr/bin/env bash
echo "curl $*" >> "$FAKE_LOG"
[ "${FAKE_OFFLINE:-0}" = 1 ] && exit 6
o=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && o="$2"; shift; done
[ -n "$o" ] && printf '#!/bin/sh\necho installing-docker >> "$FAKE_LOG"\ntouch "$FAKE_STATE/docker"\n' > "$o"
exit 0
C
cat > "$T/bin/docker" <<'D'
#!/usr/bin/env bash
[ -f "$FAKE_STATE/docker" ] || exit 127
case "$1" in
  info) [ -f "$FAKE_STATE/engine" ] || [ "${FAKE_ENGINE:-0}" = 1 ] ;;
  compose) [ "${FAKE_NOCOMPOSE:-0}" = 1 ] && exit 1; exit 0 ;;
esac
D
cat > "$T/bin/sudo" <<'S'
#!/usr/bin/env bash
echo "sudo $*" >> "$FAKE_LOG"; exec "$@"
S
cat > "$T/bin/systemctl" <<'S'
#!/usr/bin/env bash
echo "systemctl $*" >> "$FAKE_LOG"; case "$*" in *start*|*enable*) touch "$FAKE_STATE/engine" ;; esac; exit 0
S
cat > "$T/bin/usermod" <<'S'
#!/usr/bin/env bash
echo "usermod $*" >> "$FAKE_LOG"
S
cat > "$T/bin/id" <<'S'
#!/usr/bin/env bash
case "$1" in -u) echo 1000 ;; -un) echo tester ;; -nG) echo "tester wheel" ;; esac
S
cat > "$T/bin/docker-real-shim" <<'S'
S
chmod +x "$T"/bin/*
export PATH="$T/bin:$PATH" FAKE_LOG="$T/log" FAKE_STATE="$T/state" HEXTHINGS_FAKE_OS=linux HEXTHINGS_FAKE_MEM_MB=8192 HEXTHINGS_FAKE_DISK_MB=100000 HEXTHINGS_WAIT_TRIES=2
fails=0; check() { if ! eval "$2"; then echo "FAIL: $1"; fails=$((fails+1)); else echo "ok:   $1"; fi; }
fresh() { rm -rf "$T/state"; mkdir -p "$T/state"; : > "$T/log"; }
run() { bash "$ROOT/scripts/prereq.sh" "$@" > "$T/out" 2>&1 < /dev/null; rc=$?; }

fresh; touch "$T/state/docker" "$T/state/engine"; run --yes
check "everything present: exit 0, no install, no question" '[ $rc -eq 0 ] && ! grep -q -e installing -e sudo "$T/log" && grep -q "Everything needed is present" "$T/out"'

fresh; run --check
check "docker missing, --check: reports plan, installs nothing" '[ $rc -eq 2 ] && grep -q "get.docker.com" "$T/out" && ! grep -q installing "$T/log"'

fresh; run
check "docker missing, no terminal and no --yes: refuses, installs nothing" '[ $rc -eq 2 ] && grep -q "Cannot ask" "$T/out" && ! grep -q installing "$T/log"'

fresh; run --yes
check "docker missing, --yes: installs, starts, adds group, then ready or group re-login" '[ $rc -eq 0 -o $rc -eq 3 ] && grep -q installing-docker "$T/log" && grep -q "systemctl enable --now docker" "$T/log" && grep -q "usermod -aG docker tester" "$T/log"'

fresh; touch "$T/state/docker"; run --yes
check "docker present, engine stopped: only starts it, no install" '[ $rc -eq 0 ] && ! grep -q installing "$T/log" && grep -q "systemctl start docker" "$T/log"'

fresh; touch "$T/state/docker" "$T/state/engine"; FAKE_NOCOMPOSE=1 run --yes
check "compose missing is detected and the plan installs it" 'grep -q "Compose v2 is missing" "$T/out"'

fresh; FAKE_OFFLINE=1 run --yes
check "offline: clear message pointing to the air-gapped bundle, exit 1" '[ $rc -eq 1 ] && grep -q "air-gapped" "$T/out" && ! grep -q installing "$T/log"'

fresh; HEXTHINGS_FAKE_DISK_MB=2000 run --yes
check "too little disk fails before anything is installed" '[ $rc -eq 1 ] && grep -q "at least 5 GB" "$T/out" && ! grep -q installing "$T/log"'

fresh; touch "$T/state/docker" "$T/state/engine"; HEXTHINGS_FAKE_MEM_MB=2048 run --yes
check "low memory only warns" '[ $rc -eq 0 ] && grep -q "warn" "$T/out"'

fresh; HEXTHINGS_FAKE_OS=darwin run --yes
check "macOS without brew: clear manual step" '[ $rc -eq 1 ] && grep -q "docker-desktop" "$T/out"'

fresh; HEXTHINGS_FAKE_OS=plan9 run --yes
check "unknown OS refused" '[ $rc -eq 1 ]'

echo; [ $fails -eq 0 ] && echo "all prereq checks passed" || { echo "$fails failed"; exit 1; }
