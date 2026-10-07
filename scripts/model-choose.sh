#!/usr/bin/env bash
# Local AI chooser. Lists offline; downloads only with an explicit --size ID.
# Usage: bash scripts/model-choose.sh --list | --recommended | --size ID [--force]
# --force overrides estimated RAM warnings only, never disk, checksum or offline checks.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
[ -f "$HERE/docker-compose.yml" ] && ROOT=$HERE
CATALOG="$HERE/model-catalog.txt"
MODE=list; ID=; FORCE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --list) MODE=list;; --recommended) MODE=recommend;;
    --size) MODE=download; ID=${2:?model id required}; shift;;
    --force) FORCE=1;;
    --help|-h) sed -n '2,5p' "$0"; exit 0;;
    *) echo "Unknown option: $1" >&2; exit 2;;
  esac; shift
done
RAM=0; CPU=0
if [ -r /proc/meminfo ]; then RAM=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
elif command -v sysctl >/dev/null 2>&1; then RAM=$(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1048576 )); fi
CPU=$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 0)
DISK=$(df -Pk "$ROOT" | awk 'NR==2 {print int($4/1024)}')
RAM=${HEXTHINGS_FAKE_MEM_MB:-$RAM}; CPU=${HEXTHINGS_FAKE_CPU:-$CPU}; DISK=${HEXTHINGS_FAKE_DISK_MB:-$DISK}
for v in "$RAM" "$CPU" "$DISK"; do case "$v" in ''|*[!0-9]*) echo 'Could not detect resources safely' >&2; exit 1;; esac; done
fit() {
  local mb=$(( (bytes+1048575)/1048576 ))
  if [ "$DISK" -lt $((mb+10240)) ]; then echo DISK-LOW
  elif [ "$RAM" -lt "$min" ]; then echo TOO-BIG
  elif [ "$RAM" -lt "$rec" ]; then echo TIGHT
  else echo FIT; fi
}
recommended=skip
while IFS='|' read -r id label quant bytes sha url min rec licence tested note; do
  case "$id" in ''|'#'*) continue;; esac
  # Recommend only the measured model. Bigger catalog entries are untested, not upgrades we promise.
  if [ "$tested" = tested ] && [ "$(fit)" = FIT ]; then recommended=$id; fi
done < "$CATALOG"
[ "$MODE" != recommend ] || { echo "$recommended"; exit 0; }
if [ "$MODE" = list ]; then
  printf 'Detected host: RAM %s MiB | CPU %s logical cores | free disk %s MiB\n' "$RAM" "$CPU" "$DISK"
  echo 'Check Docker/VM memory limits separately. CPU speed and runtime support are not benchmarked here.'
  printf '%-16s %-10s %-12s %-14s %-14s %s\n' 'ID' 'GB download' 'RAM MiB min' 'Verdict' 'Test status' 'License'
  while IFS='|' read -r id label quant bytes sha url min rec licence tested note; do
    case "$id" in ''|'#'*) continue;; esac
    gb=$(awk -v b="$bytes" 'BEGIN {printf "%.2f",b/1000000000}')
    printf '%-16s %-10s %-12s %-14s %-14s %s\n' "$id" "$gb" "$min" "$(fit)" "$tested" "$licence"
    echo "  $label / $quant. $note"
  done < "$CATALOG"
  echo 'skip             No download; platform without local AI.'
  echo "Recommendation: $recommended (measured small model only; other fits are capacity estimates)."
  echo 'FIT/TIGHT/TOO-BIG use conservative whole-stack RAM estimates. Disk includes 10 GiB platform reserve.'
  exit 0
fi
[ "$ID" != skip ] || { echo 'AI skipped. Existing model is kept; stop ai-runtime to disable a running model.'; exit 0; }
found=0
while IFS='|' read -r id label quant bytes sha url min rec licence tested note; do
  [ "$id" = "$ID" ] && { found=1; break; }
done < "$CATALOG"
[ "$found" = 1 ] || { echo "Unknown model id: $ID" >&2; exit 2; }
[ "$sha" != manual ] || { echo "Manual-only: $ID is split GGUF, untested with this runtime; see $url. Nothing downloaded." >&2; exit 1; }
verdict=$(fit)
case "$verdict" in
  DISK-LOW) echo 'Not enough free disk for this model plus 10 GiB platform reserve' >&2; exit 1;;
  TOO-BIG|TIGHT) echo "Warning: $verdict on this host ($RAM MiB RAM; minimum estimate $min, comfortable $rec)." >&2
    [ "$FORCE" = 1 ] || { echo 'Use skip or a smaller model. --force accepts the RAM risk explicitly.' >&2; exit 1; };;
esac
[ "${HEXTHINGS_SKIP_DOWNLOAD:-0}" != 1 ] || { echo 'Download disabled by HEXTHINGS_SKIP_DOWNLOAD=1' >&2; exit 1; }
[ ! -f "$ROOT/images.tar.gz" ] || { echo 'Air-gapped bundle: network model downloads disabled. Copy a verified GGUF instead.' >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo 'curl is required for downloads' >&2; exit 1; }
url=${HEXTHINGS_MODEL_URL:-$url}; sha=${HEXTHINGS_MODEL_SHA256:-$sha}; bytes=${HEXTHINGS_MODEL_BYTES:-$bytes}
mkdir -p "$ROOT/models"
part="$ROOT/models/$ID.gguf.part"
printf 'Downloading %s (%s bytes, %s, %s). Old model kept until verification.\n' "$label" "$bytes" "$licence" "$tested"
curl -fL --retry 3 --retry-delay 3 -C - -o "$part" "$url" || { echo 'Download failed; same-model partial kept for resume' >&2; exit 1; }
got=$(sha256sum "$part" 2>/dev/null | cut -d' ' -f1 || shasum -a 256 "$part" | cut -d' ' -f1)
actual=$(wc -c < "$part" | tr -d ' ')
if [ "$got" != "$sha" ] || [ "$actual" != "$bytes" ]; then
  rm -f "$part"; echo "Model checksum mismatch or wrong size; old model unchanged ($actual bytes, sha256 $got)" >&2; exit 1
fi
mv -f "$part" "$ROOT/models/model.gguf"
printf '%s\n' "$ID" > "$ROOT/models/model.id"
limit=$((min-2560)); [ "$limit" -ge 3072 ] || limit=3072
if [ -f "$ROOT/.env" ]; then
  (umask 077; sed '/^AI_MODEL_NAME=/d; /^AI_MODEL_SHA256=/d; /^AI_MEM_LIMIT=/d' "$ROOT/.env" > "$ROOT/.env.model.tmp"
   printf '\nAI_MODEL_NAME=%s\nAI_MODEL_SHA256=%s\nAI_MEM_LIMIT=%sm\n' "$ID" "$sha" "$limit" >> "$ROOT/.env.model.tmp"
   mv "$ROOT/.env.model.tmp" "$ROOT/.env")
fi
echo 'Model verified and installed. Runtime not started. Restart ai-runtime to load the new model.'
