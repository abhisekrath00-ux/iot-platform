#!/usr/bin/env bash
# Chooser tests: fake download, real SHA/size checks; no model inference or real Docker.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/w/scripts" "$T/bin"
cp "$ROOT/scripts/model-choose.sh" "$ROOT/scripts/model-catalog.txt" "$T/w/scripts/"
cat > "$T/bin/curl" <<'C'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE_LOG"
[ "${FAKE_FAIL:-0}" = 0 ] || exit 22
while [ $# -gt 0 ]; do if [ "$1" = -o ]; then printf fake-model > "$2"; exit; fi; shift; done
C
chmod +x "$T/bin/curl"
export PATH="$T/bin:$PATH" FAKE_LOG="$T/log" HEXTHINGS_FAKE_MEM_MB=5120 HEXTHINGS_FAKE_CPU=2 HEXTHINGS_FAKE_DISK_MB=100000
export HEXTHINGS_MODEL_BYTES=10 HEXTHINGS_MODEL_SHA256=$(printf fake-model | sha256sum | cut -d' ' -f1)
: > "$FAKE_LOG"
run() { bash "$T/w/scripts/model-choose.sh" "$@" > "$T/out" 2>&1; }
fails=0; check() { if eval "$2"; then echo "ok: $1"; else echo "FAIL: $1"; fails=$((fails+1)); fi; }
run --list
check 'five sizes and skip with resources/licenses/test labels, no network' '[ $(grep -c "^qwen3\|^gpt-oss" "$T/out") -eq 5 ] && grep -q "RAM 5120 MiB" "$T/out" && grep -q "Apache-2.0" "$T/out" && grep -q "untested-here" "$T/out" && grep -q "^skip" "$T/out" && [ ! -s "$FAKE_LOG" ]'
run --recommended; check '5 GiB recommendation is measured 1.7B' 'grep -qx qwen3-1.7b "$T/out"'
HEXTHINGS_FAKE_MEM_MB=4096 run --recommended; check '4 GiB defaults to skip, not a tight download' 'grep -qx skip "$T/out"'
run --size nonsense && rc=0 || rc=$?; check 'unknown model refused' '[ $rc -ne 0 ] && [ ! -s "$FAKE_LOG" ]'
run --size gpt-oss-120b --force && rc=0 || rc=$?; check 'split 120B stays manual even with force' '[ $rc -ne 0 ] && grep -q Manual-only "$T/out" && [ ! -s "$FAKE_LOG" ]'
run --size qwen3-8b && rc=0 || rc=$?; check 'oversized RAM model refused' '[ $rc -ne 0 ] && grep -q TOO-BIG "$T/out" && [ ! -s "$FAKE_LOG" ]'
HEXTHINGS_FAKE_MEM_MB=4096 run --size qwen3-1.7b && rc=0 || rc=$?; check 'tight RAM requires explicit override' '[ $rc -ne 0 ] && grep -q TIGHT "$T/out"'
HEXTHINGS_FAKE_DISK_MB=1 run --size qwen3-1.7b --force && rc=0 || rc=$?; check 'force never bypasses disk' '[ $rc -ne 0 ] && [ ! -s "$FAKE_LOG" ]'
HEXTHINGS_SKIP_DOWNLOAD=1 run --size qwen3-1.7b && rc=0 || rc=$?; check 'offline switch prevents network' '[ $rc -ne 0 ] && [ ! -s "$FAKE_LOG" ]'
touch "$T/w/images.tar.gz"; run --size qwen3-1.7b && rc=0 || rc=$?; check 'airgap bundle prevents network' '[ $rc -ne 0 ] && [ ! -s "$FAKE_LOG" ]'; rm "$T/w/images.tar.gz"
mkdir -p "$T/w/models"; echo old > "$T/w/models/model.gguf"; echo 'JWT_SIGNING_SECRET=keep-me' > "$T/w/.env"
HEXTHINGS_MODEL_SHA256=bad run --size qwen3-1.7b && rc=0 || rc=$?; check 'bad SHA leaves old model and removes damaged partial' '[ $rc -ne 0 ] && grep -qx old "$T/w/models/model.gguf" && [ ! -f "$T/w/models/qwen3-1.7b.gguf.part" ]'
HEXTHINGS_MODEL_BYTES=11 run --size qwen3-1.7b && rc=0 || rc=$?; check 'wrong size leaves old model' '[ $rc -ne 0 ] && grep -qx old "$T/w/models/model.gguf"'
FAKE_FAIL=1 run --size qwen3-1.7b && rc=0 || rc=$?; check 'failed network leaves old model' '[ $rc -ne 0 ] && grep -qx old "$T/w/models/model.gguf"'
run --size qwen3-1.7b; check 'verified install records id and runtime config, preserves secrets' '[ "$(cat "$T/w/models/model.gguf")" = fake-model ] && grep -qx qwen3-1.7b "$T/w/models/model.id" && grep -q "JWT_SIGNING_SECRET=keep-me" "$T/w/.env" && grep -q "AI_MODEL_NAME=qwen3-1.7b" "$T/w/.env" && grep -q "AI_MEM_LIMIT=3072m" "$T/w/.env" && grep -q -- "-C -" "$FAKE_LOG"'
HEXTHINGS_FAKE_MEM_MB=4096 run --size qwen3-1.7b --force; check 'force permits explicit RAM-risk selection' 'grep -q verified "$T/out"'
run --size skip; check 'skip keeps existing file and does not start runtime' '[ "$(cat "$T/w/models/model.gguf")" = fake-model ] && grep -q skipped "$T/out"'
[ "$fails" = 0 ] || exit 1
echo 'All chooser checks passed'
