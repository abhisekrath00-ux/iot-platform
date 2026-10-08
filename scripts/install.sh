#!/usr/bin/env bash
# Guided installer for HexThings (Linux, macOS, WSL).
#
#   ./install.sh                 asks a few questions, then does everything
#   ./install.sh --yes           no questions: defaults, generated secrets
#
# It checks the host, writes .env with generated secrets, loads or builds the images, starts the
# stack, waits until it is healthy, creates the first workspace and administrator, and prints
# where to sign in. It never overwrites an existing .env or touches the internet when run from an
# air-gapped bundle (images.tar.gz next to it). Everything it does is logged to install.log.
#
# Options: --yes  --admin-email E  --workspace ID  --web-port N  --ai-model FILE.gguf  --ai-download  --ai-model-size ID  --ai-force  --no-ai
#          --skip-preflight-warnings
set -euo pipefail
cd "$(dirname "$0")"
[ -f docker-compose.yml ] || cd ..   # run from scripts/ inside a source checkout
LOG=install.log
: > "$LOG"

YES=0; ADMIN_EMAIL=""; WORKSPACE=""; WEB_PORT=""; AI_MODEL=""; AI_DL=0; NO_AI=0; AI_SIZE=""; AI_FORCE=0
CHOOSER=scripts/model-choose.sh; [ -f "$CHOOSER" ] || CHOOSER=./model-choose.sh
while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) YES=1 ;;
    --admin-email) ADMIN_EMAIL="${2:?}"; shift ;;
    --workspace) WORKSPACE="${2:?}"; shift ;;
    --web-port) WEB_PORT="${2:?}"; shift ;;
    --ai-model) AI_MODEL="${2:?}"; shift ;;
    --ai-download) AI_DL=1 ;;
    --ai-model-size) AI_SIZE="${2:?}"; AI_DL=1; shift ;;
    --ai-force) AI_FORCE=1 ;;
    --no-ai) NO_AI=1 ;;
    --help|-h) sed -n 2,14p "$0"; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
  shift
done

# ---- look and feel ----------------------------------------------------------------------------
# Colour only on a real terminal (and not when NO_COLOR is set); Unicode only when the locale says
# UTF-8. Everything else gets plain ASCII with the same words, so logs and CI stay readable.
COLOR=0; UNI=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then COLOR=1; fi
case "${LC_ALL:-${LC_CTYPE:-${LANG:-}}}" in *[Uu][Tt][Ff]-8*|*[Uu][Tt][Ff]8*) UNI=1 ;; esac
[ "${HEXTHINGS_ASCII:-}" = 1 ] && UNI=0
if [ "$COLOR" = 1 ]; then
  B=$'\033[1m'; D=$'\033[2m'; N=$'\033[0m'; G=$'\033[32m'; Y=$'\033[33m'; R=$'\033[31m'
  C1=$'\033[38;5;45m'; C2=$'\033[38;5;39m'; C3=$'\033[38;5;33m'; C4=$'\033[38;5;63m'; C5=$'\033[38;5;99m'
else B=; D=; N=; G=; Y=; R=; C1=; C2=; C3=; C4=; C5=; fi
if [ "$UNI" = 1 ]; then I_OK="✔"; I_WARN="▲"; I_FAIL="✖"; BAR_ON="█"; BAR_OFF="░"; SPIN=(⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏)
else I_OK="ok"; I_WARN="warn"; I_FAIL="FAIL"; BAR_ON="#"; BAR_OFF="-"; SPIN=('|' '/' '-' '\'); fi
plain() { printf '%s\n' "$(printf '%s' "$*" | sed $'s/\033\\[[0-9;]*m//g')" >> "$LOG"; }
say()  { printf '%s\n' "$*"; plain "$*"; }
ok()   { say "  ${G}${I_OK}${N}  $*"; }
warn() { say "  ${Y}${I_WARN}${N}  $*"; WARNINGS=$((WARNINGS+1)); }
fail() { say "  ${R}${I_FAIL}${N}  $*"; say ""; say "Nothing was started. Fix the line above and run the installer again; it is safe to re-run."; exit 1; }
WARNINGS=0
TOTAL_STEPS=4; STEP=0
bar() { local n=$1 t=$2 w=20 i out=""; for ((i=0;i<w;i++)); do if [ $((i*t)) -lt $((n*w)) ]; then out+="$BAR_ON"; else out+="$BAR_OFF"; fi; done; printf '%s' "$out"; }
step() { # step "Title"
  STEP=$((STEP+1)); say ""
  if [ "$UNI" = 1 ]; then say "${C2}${B}┃${N} ${B}$STEP/$TOTAL_STEPS  $1${N}   ${C3}$(bar "$STEP" "$TOTAL_STEPS")${N}"
  else say "== $STEP/$TOTAL_STEPS  $1  [$(bar "$STEP" "$TOTAL_STEPS")] =="; fi
}
# spin "message" command...: runs the command with output in the log, animates while it works.
spin() {
  local msg=$1; shift
  if [ "$COLOR" = 0 ]; then say "  ... $msg"; "$@" >> "$LOG" 2>&1; return $?; fi
  "$@" >> "$LOG" 2>&1 & local pid=$! i=0 t0=$SECONDS
  while kill -0 "$pid" 2>/dev/null; do
    printf '\r  %s%s%s  %s %s(%ds)%s\033[K' "$C1" "${SPIN[$((i%${#SPIN[@]}))]}" "$N" "$msg" "$D" $((SECONDS-t0)) "$N"; i=$((i+1)); sleep 0.12
  done
  local rc=0; wait "$pid" || rc=$?
  printf '\r\033[K'; return $rc
}
# glyph LETTER ROW -> 7-row font for "HexThings" ('#' is a filled cell). Plain case, so it works in bash 3.2 (macOS).
glyph() {
  case "$1$2" in
    H0|H1|H2) echo "#...#";; H3) echo "#####";; H4|H5) echo "#...#";; H6) echo ".....";;
    e0) echo ".....";; e1) echo ".###.";; e2) echo "#...#";; e3) echo "#####";; e4) echo "#....";; e5) echo ".####";; e6) echo ".....";;
    x0) echo ".....";; x1) echo "#...#";; x2) echo ".#.#.";; x3) echo "..#..";; x4) echo ".#.#.";; x5) echo "#...#";; x6) echo ".....";;
    T0) echo "#####";; T1|T2|T3|T4|T5) echo "..#..";; T6) echo ".....";;
    h0|h1) echo "#....";; h2) echo "#.##.";; h3) echo "##..#";; h4|h5) echo "#...#";; h6) echo ".....";;
    i0) echo ".#.";; i1) echo "...";; i2) echo "##.";; i3|i4) echo ".#.";; i5) echo "###";; i6) echo "...";;
    n0|n1) echo ".....";; n2) echo "#.##.";; n3) echo "##..#";; n4|n5) echo "#...#";; n6) echo ".....";;
    g0) echo ".....";; g1) echo ".####";; g2|g3) echo "#...#";; g4) echo ".####";; g5) echo "....#";; g6) echo ".###.";;
    s0) echo ".....";; s1) echo ".####";; s2) echo "#....";; s3) echo ".###.";; s4) echo "....#";; s5) echo "####.";; s6) echo ".....";;
  esac
}
# wordmark: big gradient "HexThings" with a short row-by-row reveal. Plain "HexThings" when there is no colour or the window is narrow.
wordmark() { # wordmark "subtitle"
  local sub=${1:-} cols=${COLUMNS:-$(tput cols 2>/dev/null || echo 80)}
  if [ "$COLOR" = 0 ] || [ "$cols" -lt 56 ]; then printf '%s\n' "" "  HexThings   $sub" ""; return; fi
  local letters="HexThings" blk="#" r k ch g line i c pal
  [ "$UNI" = 1 ] && blk="█"
  if [ "$(tput colors 2>/dev/null || echo 8)" -ge 256 ]; then pal=(51 51 45 39 33 63 99 135 171)
  else pal=(36 36 36 34 34 34 35 35 35); fi
  echo ""
  for r in 0 1 2 3 4 5 6; do
    printf '  '
    for ((k=0;k<${#letters};k++)); do
      ch=${letters:$k:1}; g=$(glyph "$ch" "$r"); line=""
      for ((i=0;i<${#g};i++)); do if [ "${g:$i:1}" = "#" ]; then line+="$blk"; else line+=" "; fi; done
      if [ "$(tput colors 2>/dev/null || echo 8)" -ge 256 ]; then printf '\033[38;5;%sm%s \033[0m' "${pal[$k]}" "$line"
      else printf '\033[%sm%s \033[0m' "${pal[$k]}" "$line"; fi
      [ -t 1 ] && sleep 0.012
    done
    echo ""
  done
  echo ""; printf '  %sIndustrial IoT platform%s%s%s\n' "$D" "$([ -n "$sub" ] && printf '  %s  %s' "$blk" "$sub")" "" "$N"
}
footer() {
  local heart="<3"; [ "$UNI" = 1 ] && heart="❤"
  if [ "$COLOR" = 1 ]; then printf '  %sMade with %s\033[31m%s%s %sby Hexmon Technology%s\n' "$D" "$N" "$heart" "$N" "$D" "$N"; else printf '  Made with %s by Hexmon Technology\n' "$heart"; fi
}
banner() {
  wordmark "guided installer"; footer; echo ""
  plain "HexThings guided installer"
}
ask() { # ask "question" default -> echoes answer
  if [ "$YES" = 1 ] || [ ! -t 0 ]; then echo "$2"; return; fi
  local a; read -r -p "  ${C2}?${N} $1 ${D}[$2]${N}: " a || true; echo "${a:-$2}"
}
rand_alnum() { LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c "$1" || true; }
rand_b64()   { head -c 32 /dev/urandom | base64 | tr -d '\n'; }

[ "$NO_AI" = 0 ] || { AI_DL=0; AI_SIZE=""; }
[ "$AI_SIZE" != skip ] || { NO_AI=1; AI_DL=0; }
[ "$AI_DL" = 0 ] || [ -z "$AI_MODEL" ] || { echo "Choose --ai-model OR --ai-model-size, not both" >&2; exit 2; }
banner
step "Checking this machine"

# ---- preflight --------------------------------------------------------------------------------
command -v docker >/dev/null 2>&1 || fail "Docker is not installed. Install Docker Engine (or Docker Desktop) and run this again."
docker info >/dev/null 2>&1 || fail "Docker is installed but not running, or your user may not use it. Start Docker, or add yourself to the docker group."
ok "Docker is running"
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is missing ('docker compose version' fails). Install the compose plugin."
ok "Docker Compose v2 present"

BUNDLE=0; [ -f images.tar.gz ] && BUNDLE=1
if [ "$BUNDLE" = 1 ]; then ok "air-gapped bundle found: images load from images.tar.gz, no internet needed"; else ok "source checkout: images are built here (needs internet for base images unless already cached)"; fi

mem_mb=0
if [ -r /proc/meminfo ]; then mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
elif command -v sysctl >/dev/null 2>&1; then mem_mb=$(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1048576 )); fi
if [ "$mem_mb" -eq 0 ]; then warn "could not read installed memory"
elif [ "$mem_mb" -lt 4096 ]; then warn "${mem_mb} MB RAM: the platform needs about 4 GB (Elasticsearch is the heaviest part)"
else ok "${mem_mb} MB RAM"; fi

year=${HEXTHINGS_FAKE_YEAR:-$(date -u +%Y)}
if [ "$year" -lt 2026 ] 2>/dev/null; then warn "the system clock says the year $year: TLS certificates, sign-in tokens and log timestamps will misbehave. Fix the clock, then run this again."; fi

free_gb=$(df -Pk . | awk 'NR==2 {print int($4/1048576)}')
if [ "${free_gb:-0}" -lt 10 ]; then warn "only ${free_gb} GB free in this folder; plan for 10 GB or more"; else ok "${free_gb} GB free disk"; fi

port_busy() {
  if command -v ss >/dev/null 2>&1; then ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$1\$"
  elif command -v lsof >/dev/null 2>&1; then lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
  else return 1; fi
}
WEB_PORT="${WEB_PORT:-$( [ -f .env ] && sed -n 's/^WEB_PORT=//p' .env | head -1 || true )}"
WEB_PORT="${WEB_PORT:-8080}"
RUNNING=0; docker compose ps --status running -q 2>/dev/null | grep -q . && RUNNING=1
if [ "$RUNNING" = 1 ]; then ok "an existing install is running; this will update it in place"
else
  for p in "$WEB_PORT" 8000 1883 5432; do
    if port_busy "$p"; then
      [ "$p" = "$WEB_PORT" ] && fail "port $p (web) is already in use. Re-run with --web-port N, or stop what uses it."
      warn "port $p is in use by something else; the service that needs it may fail to start"
    fi
  done
  ok "ports free"
fi

# ---- questions --------------------------------------------------------------------------------
step "Setup"
NEW_ENV=0
if [ -f .env ]; then
  ok ".env exists and is kept as it is (secrets are not regenerated)"
else
  NEW_ENV=1
  umask 077
  {
    sed -e 's|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD='"$(rand_alnum 32)"'|' \
        -e 's|^JWT_SIGNING_SECRET=.*|JWT_SIGNING_SECRET='"$(rand_alnum 64)"'|' \
        -e 's|^SECRETS_KEY=.*|SECRETS_KEY='"$(rand_b64)"'|' \
        -e 's|^WEB_PORT=.*|WEB_PORT='"$WEB_PORT"'|' \
        -e '/^DATABASE_URL=/d' .env.example
  } > .env.tmp
  [ -s .env.tmp ] || { rm -f .env.tmp; fail "could not read .env.example"; }
  # the compose file builds DATABASE_URL itself from the POSTGRES_* values
  pw=$(sed -n 's/^POSTGRES_PASSWORD=//p' .env.tmp | head -1)
  { cat .env.tmp; echo "# Installer: the first administrator signs in with a password. Use SSO for production (docs/security.md)."; echo "LOCAL_LOGIN=1"; } > .env
  rm -f .env.tmp; chmod 600 .env
  [ -n "$pw" ] || fail ".env was written without a database password"
  ok ".env written with generated secrets (mode 600)"
fi

if [ -z "$WORKSPACE" ]; then WORKSPACE=$(ask "Name for your first workspace (lowercase letters, digits, dashes)" "my-plant"); fi
if [ -z "$ADMIN_EMAIL" ]; then ADMIN_EMAIL=$(ask "Administrator email (you must change it at first sign-in if it stays the default)" "admin@hexthings.com"); fi
printf '%s' "$WORKSPACE" | grep -Eq '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$' || fail "workspace '$WORKSPACE' must be 3-40 characters: a-z, 0-9, dashes"
printf '%s' "$ADMIN_EMAIL" | grep -Eq '^[^@ ]+@[^@ ]+\.[^@ ]+$' || fail "'$ADMIN_EMAIL' is not an email address"

COMPOSE_ARGS=()
if [ "$NO_AI" = 0 ]; then
  if [ -z "$AI_MODEL" ] && [ -f model.gguf ]; then AI_MODEL=model.gguf; fi
  if [ -z "$AI_MODEL" ] && [ -f models/model.gguf ]; then AI_MODEL=models/model.gguf; fi
  if [ -z "$AI_MODEL" ] && [ "$AI_DL" = 0 ] && [ "$YES" = 0 ] && [ -t 0 ]; then
    bash "$CHOOSER" --list | tee -a "$LOG"
    a=$(ask "Local AI: model ID, skip, or path to your own .gguf (download needs internet once)" "skip")
    case "$a" in skip|n|N|no|"") ;; *.gguf) AI_MODEL="$a" ;; *) AI_SIZE="$a"; AI_DL=1 ;; esac
  fi
  if [ "$AI_DL" = 1 ]; then
    AI_SIZE=${AI_SIZE:-qwen3-1.7b}
    bash "$CHOOSER" --list | tee -a "$LOG"
    extra=(); [ "$AI_FORCE" = 0 ] || extra=(--force)
    bash "$CHOOSER" --size "$AI_SIZE" ${extra[@]+"${extra[@]}"} | tee -a "$LOG" || fail "model selection/download failed; existing model kept"
    AI_MODEL=models/model.gguf
  fi
  if [ -n "$AI_MODEL" ]; then
    [ -f "$AI_MODEL" ] || fail "model file not found: $AI_MODEL"
    [ "$mem_mb" -ge 5120 ] || warn "${mem_mb} MB host RAM: model plus platform may be tight; check Docker memory limits too"
    mkdir -p models
    [ "$(cd "$(dirname "$AI_MODEL")" && pwd)/$(basename "$AI_MODEL")" = "$(pwd)/models/model.gguf" ] || cp "$AI_MODEL" models/model.gguf
    AI_SHA=$(sha256sum models/model.gguf 2>/dev/null | cut -d' ' -f1 || shasum -a 256 models/model.gguf | cut -d' ' -f1)
    export AI_MODEL_SHA256="$AI_SHA"
    if [ -n "$AI_MODEL" ] && [ "$AI_DL" = 0 ]; then
      (umask 077; sed "/^AI_MODEL_SHA256=/d" .env > .env.model.tmp; printf '\nAI_MODEL_SHA256=%s\n' "$AI_SHA" >> .env.model.tmp; mv .env.model.tmp .env)
    fi
    if [ -z "$AI_SIZE" ] && [ -f models/model.id ]; then AI_SIZE=$(head -1 models/model.id); fi
    [ -z "$AI_SIZE" ] || printf '%s\n' "$AI_SIZE" > models/model.id
    COMPOSE_ARGS=(--profile ai)
    ok "local AI model ready (sha256 ${AI_SHA:0:12}...); AI stays optional and off until enabled in Settings"
  else
    ok "no local AI model (the platform works the same without it; add one later, docs/ai-runtime.md)"
  fi
fi

# ---- install ----------------------------------------------------------------------------------
step "Installing (the first run takes several minutes)"
load_images() { gunzip -c images.tar.gz | docker load; }
if [ "$BUNDLE" = 1 ]; then
  if [ -f SHA256SUMS ]; then
    (sha256sum -c SHA256SUMS >> "$LOG" 2>&1 || shasum -a 256 -c SHA256SUMS >> "$LOG" 2>&1) || fail "checksum mismatch: the bundle is damaged or was changed. Copy it again."
    ok "bundle checksums verified"
  fi
  spin "loading images (offline)" load_images || fail "could not load images (see $LOG)"
  ok "images loaded"
  spin "starting services" docker compose ${COMPOSE_ARGS[@]+"${COMPOSE_ARGS[@]}"} up -d --no-build || fail "docker compose up failed (see $LOG)"
else
  spin "building and starting services" docker compose ${COMPOSE_ARGS[@]+"${COMPOSE_ARGS[@]}"} up -d --build || fail "docker compose up failed (see $LOG)"
fi
ok "services started"

http_ok() { if command -v curl >/dev/null 2>&1; then curl -fsS -m 3 "$1" >/dev/null 2>&1; else wget -q -T 3 -O /dev/null "$1" 2>/dev/null; fi; }
wait_health() { local _; for _ in $(seq 1 90); do http_ok "http://localhost:${WEB_PORT}/healthz" && return 0; sleep 2; done; return 1; }
up=0; spin "waiting for the platform to answer" wait_health && up=1
[ "$up" = 1 ] || fail "the platform did not become healthy within 3 minutes. Run: docker compose logs api  (details in $LOG)"
ok "platform is healthy"

# ---- first workspace and administrator --------------------------------------------------------
step "First workspace"
if [ -f install-credentials.txt ]; then warn "install-credentials.txt from an older install holds a password in plain text. Change that password in the app, then delete the file."; fi
PASS=""
if docker compose exec -T api /bin/tenantctl list 2>>"$LOG" | grep -q "^${WORKSPACE} "; then
  ok "workspace '$WORKSPACE' already exists; the administrator was not changed"
else
  # Fresh install only: the documented first-run login (hashed by tenantctl, flagged so the first sign-in must
  # replace it). No password is generated, written to a file or logged. An existing workspace is never touched.
  if docker compose exec -T api /bin/tenantctl create --id "$WORKSPACE" --name "$WORKSPACE" --admin-email "$ADMIN_EMAIL" --default-credentials >> "$LOG" 2>&1; then
    PASS="Hex@2026"
    ok "workspace '$WORKSPACE' and administrator created"
  else
    fail "could not create the workspace (see $LOG)"
  fi
fi

if [ "${#COMPOSE_ARGS[@]}" -gt 0 ]; then
  if docker compose exec -T api /bin/tenantctl ai-connect --tenant "$WORKSPACE" --model "${AI_SIZE:-qwen3-1.7b}" >> "$LOG" 2>&1; then
    ok "AI assistant connected to the local model (Settings > AI shows it; the first answer is slow while the model loads)"
  else
    warn "could not connect the AI assistant automatically; set it in Settings > AI (base URL http://ai-runtime:8090/v1, model qwen3-1.7b)"
  fi
fi

# ---- done -------------------------------------------------------------------------------------
say ""
if [ "$UNI" = 1 ]; then
  W=62; line=$(printf '─%.0s' $(seq 1 $W))
  row() { local t="$1" vis; vis=$(printf '%s' "$t" | sed $'s/\033\\[[0-9;]*m//g'); printf '%s\n' "${C3}│${N} $t$(printf ' %.0s' $(seq 1 $((W-1-${#vis}))))${C3}│${N}"; }
  say "${C3}╭${line}╮${N}"
  row "${G}${B}${I_OK}  HexThings is installed${N}"
  row ""
  row "${B}Open${N}       http://localhost:${WEB_PORT}"
  row "${B}Workspace${N}  ${WORKSPACE}"
  row "${B}Email${N}      ${ADMIN_EMAIL}"
  [ -z "$PASS" ] || row "${B}Password${N}   ${PASS}"
  say "${C3}╰${line}╯${N}"; footer
else
  say "Installed. HexThings is ready."; footer
  say ""
  say "  Open:      http://localhost:${WEB_PORT}"
  say "  Workspace: ${WORKSPACE}"
  say "  Email:     ${ADMIN_EMAIL}"
  [ -z "$PASS" ] || printf "%s\n" "  Password:  ${PASS}"
fi
[ -z "$PASS" ] || say "  ${D}(first-run password: you are asked to change the email and password right after signing in; it is not saved anywhere)${N}"
say ""
say "  Stop:      docker compose stop        Start: docker compose start"
say "  Back up:   ./scripts/backup.sh        Logs:  docker compose logs -f api"
say "  Next:      Add device (guided scan and onboarding), Settings (email, SSO, AI)"
[ "$WARNINGS" -eq 0 ] || say "  Note:      $WARNINGS warning(s) above. The log is in $LOG."
exit 0
