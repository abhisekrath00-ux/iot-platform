#!/usr/bin/env bash
# Tests edge/packaging/install.sh address handling with a fake curl and a fake install-linux.sh.
# Checks fallback selection and that the full address list reaches the agent. No real install happens.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/pkg"
cp "$ROOT/edge/packaging/install.sh" "$T/pkg/"
cat > "$T/pkg/install-linux.sh" <<'I'
#!/usr/bin/env bash
echo "ARGS: $*" > "$FAKE_OUT"
I
chmod +x "$T/pkg/install-linux.sh" "$T/pkg/install.sh"
cat > "$T/bin/curl" <<'C'
#!/usr/bin/env bash
# only addresses containing GOOD answer
case "$*" in *GOOD*) exit 0 ;; *) exit 22 ;; esac
C
chmod +x "$T/bin/curl"
export PATH="$T/bin:$PATH" FAKE_OUT="$T/out" HEXMON_SKIP_SYSTEMD=1
fail() { echo "FAIL: $*"; exit 1; }

out=$(cd "$T/pkg" && ./install.sh --server "http://down.example:1,http://GOOD.example:2" --code C --serial S --yes 2>&1) || fail "fallback run exited non-zero: $out"
echo "$out" | grep -q "no answer from http://down.example:1" || fail "should report the dead primary"
echo "$out" | grep -q "server answers at http://GOOD.example:2" || fail "should use the fallback"
grep -q -- "-claim-api http://down.example:1,http://GOOD.example:2 " "$T/out" || fail "agent must get the whole list: $(cat "$T/out")"

if (cd "$T/pkg" && ./install.sh --server "http://down.example:1,http://down2.example:2" --code C --serial S --yes >"$T/o2" 2>&1); then fail "all-down must fail"; fi
grep -q "cannot reach any server address" "$T/o2" || fail "all-down message: $(cat "$T/o2")"

lo=$(cd "$T/pkg" && ./install.sh --server "http://localhost:8000,http://GOOD.example:2" --code C --serial S --yes 2>&1)
echo "$lo" | grep -q "loopback" || fail "loopback warning missing"
if (cd "$T/pkg" && ./install.sh --server "ftp://x" --code C --serial S --yes >/dev/null 2>&1); then fail "bad scheme must fail"; fi
echo "edge install address tests passed"
