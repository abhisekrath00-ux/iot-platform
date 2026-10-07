#!/usr/bin/env bash
# Tests scripts/install.sh against fake docker/curl programs: no Docker is needed or touched.
# It checks the installer's own logic (preflight failures, secret generation, no overwrite,
# credentials file, idempotence). It does NOT prove the compose stack starts; that was never run.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/w/scripts"
cp "$ROOT/scripts/install.sh" "$T/w/scripts/"; cp "$ROOT/.env.example" "$ROOT/docker-compose.yml" "$T/w/"
cat > "$T/bin/docker" <<'D'
#!/usr/bin/env bash
echo "docker $*" >> "$FAKE_LOG"
case "$1 $2" in
  "info "*) [ "${FAKE_DOCKER_DOWN:-0}" = 1 ] && exit 1; exit 0 ;;
  "compose version") exit 0 ;;
  "compose ps") [ "${FAKE_RUNNING:-0}" = 1 ] && echo abc; exit 0 ;;
esac
if [ "$1" = compose ]; then
  case " $* " in
    *"tenantctl list"*) [ "${FAKE_TENANT_EXISTS:-0}" = 1 ] && echo "my-plant  my-plant users=1 devices=0"; exit 0 ;;
    *"tenantctl create"*) cat > "$FAKE_STDIN"; exit 0 ;;
  esac
fi
exit 0
D
cat > "$T/bin/curl" <<'C'
#!/usr/bin/env bash
# fake curl: when asked to write a file (-o F), writes a fake model; otherwise succeeds silently
while [ $# -gt 0 ]; do [ "$1" = -o ] && { printf 'fake-model' > "$2"; break; }; shift; done
exit 0
C
chmod +x "$T/bin/curl"; printf '' ; chmod +x "$T/bin/docker" "$T/bin/curl"
export PATH="$T/bin:$PATH" FAKE_LOG="$T/log" FAKE_STDIN="$T/stdin"; : > "$FAKE_LOG"
fails=0; check() { if ! eval "$2"; then echo "FAIL: $1"; fails=$((fails+1)); else echo "ok:   $1"; fi; }
run() { (cd "$T/w" && bash scripts/install.sh --web-port 18431 "$@" > "$T/out" 2>&1); }

FAKE_DOCKER_DOWN=1 run --yes && rc=0 || rc=$?
check "docker not running stops before anything starts" '[ $rc -ne 0 ] && ! grep -q "compose up" "$FAKE_LOG" && grep -q "not running" "$T/out"'

rm -f "$T/w/.env"; : > "$FAKE_LOG"
run --yes --admin-email boss@plant.example --workspace north-plant && rc=0 || rc=$?
check "happy path exits 0" '[ $rc -eq 0 ]'
check ".env has no placeholder secrets" '! grep -q "change-me" "$T/w/.env" && grep -q "^LOCAL_LOGIN=1" "$T/w/.env"'
check ".env is private" '[ "$(stat -c %a "$T/w/.env")" = 600 ]'
check "secrets are long and random" '[ $(sed -n "s/^JWT_SIGNING_SECRET=//p" "$T/w/.env" | wc -c) -ge 64 ] && [ $(sed -n "s/^SECRETS_KEY=//p" "$T/w/.env" | base64 -d | wc -c) -eq 32 ]'
check "compose up ran with build (source mode)" 'grep -q "compose up -d --build" "$FAKE_LOG"'
check "admin password reached tenantctl on stdin, not argv" '[ $(wc -c < "$T/stdin") -eq 21 ] && ! grep -q "$(head -1 "$T/stdin")" "$FAKE_LOG"'
check "credentials file is private and holds that password" '[ "$(stat -c %a "$T/w/install-credentials.txt")" = 600 ] && grep -q "$(head -1 "$T/stdin")" "$T/w/install-credentials.txt"'
check "success screen shows URL" 'grep -q "http://localhost:18431" "$T/out" && grep -q "north-plant" "$T/out"'

cp "$T/w/.env" "$T/env.before"; : > "$FAKE_LOG"
FAKE_TENANT_EXISTS=1 run --yes --workspace my-plant && rc=0 || rc=$?
check "re-run keeps .env and secrets" 'cmp -s "$T/w/.env" "$T/env.before"'
check "re-run does not create a second administrator" '! grep -q "tenantctl create" "$FAKE_LOG" && grep -q "already exists" "$T/out"'

run --yes --workspace "Bad Name" && rc=0 || rc=$?
check "bad workspace name is refused" '[ $rc -ne 0 ] && grep -q "must be 3-40" "$T/out"'
run --yes --admin-email nope --workspace ok-name && rc=0 || rc=$?
check "bad email is refused" '[ $rc -ne 0 ]'

HEXTHINGS_FAKE_YEAR=2019 run --yes --workspace ok-name --admin-email a@b.example && rc=0 || rc=$?
check "a wildly wrong clock warns but does not block" '[ $rc -eq 0 ] && grep -q "system clock says the year 2019" "$T/out"'

echo fake > "$T/w/m.gguf"; : > "$FAKE_LOG"
run --yes --workspace ok-name --admin-email a@b.example --ai-model "$T/w/m.gguf" && rc=0 || rc=$?
check "AI model enables the ai profile and is copied" '[ $rc -eq 0 ] && grep -q "compose --profile ai up" "$FAKE_LOG" && [ -f "$T/w/models/model.gguf" ]'
rm -rf "$T/w/models" "$T/w/m.gguf"; : > "$FAKE_LOG"
SHA=$(printf 'fake-model' | sha256sum | cut -d' ' -f1)
HEXTHINGS_MODEL_SHA256=0000 run --yes --workspace ok-name --admin-email a@b.example --ai-download && rc=0 || rc=$?
check "a damaged model download is refused" '[ $rc -ne 0 ] && grep -q "checksum mismatch" "$T/out" && [ ! -f "$T/w/models/model.gguf" ]'
: > "$FAKE_LOG"
HEXTHINGS_MODEL_SHA256=$SHA run --yes --workspace ok-name --admin-email a@b.example --ai-download && rc=0 || rc=$?
check "--ai-download fetches, verifies, enables the ai profile" '[ $rc -eq 0 ] && [ -f "$T/w/models/model.gguf" ] && grep -q "compose --profile ai up" "$FAKE_LOG"'
check "the assistant is connected automatically" 'grep -q "tenantctl ai-connect --tenant ok-name" "$FAKE_LOG"'
rm -rf "$T/w/models"; : > "$FAKE_LOG"
run --yes --workspace ok-name --admin-email a@b.example && rc=0 || rc=$?
check "without --ai-download nothing is downloaded" '[ $rc -eq 0 ] && [ ! -f "$T/w/models/model.gguf" ] && ! grep -q "ai-connect" "$FAKE_LOG"'
[ $fails -eq 0 ] && echo "all installer checks passed" || { echo "$fails failed"; exit 1; }
