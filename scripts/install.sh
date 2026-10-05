#!/usr/bin/env bash
# Guided installer for the Hexmon IoT platform (Linux, macOS, WSL).
#
#   ./install.sh                 asks a few questions, then does everything
#   ./install.sh --yes           no questions: defaults, generated secrets
#
# It checks the host, writes .env with generated secrets, loads or builds the images, starts the
# stack, waits until it is healthy, creates the first workspace and administrator, and prints
# where to sign in. It never overwrites an existing .env or touches the internet when run from an
# air-gapped bundle (images.tar.gz next to it). Everything it does is logged to install.log.
#
# Options: --yes  --admin-email E  --workspace ID  --web-port N  --ai-model FILE.gguf  --no-ai
#          --skip-preflight-warnings
set -euo pipefail
cd "$(dirname "$0")"
[ -f docker-compose.yml ] || cd ..   # run from scripts/ inside a source checkout
LOG=install.log
: > "$LOG"

YES=0; ADMIN_EMAIL=""; WORKSPACE=""; WEB_PORT=""; AI_MODEL=""; NO_AI=0
while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) YES=1 ;;
    --admin-email) ADMIN_EMAIL="${2:?}"; shift ;;
    --workspace) WORKSPACE="${2:?}"; shift ;;
    --web-port) WEB_PORT="${2:?}"; shift ;;
    --ai-model) AI_MODEL="${2:?}"; shift ;;
    --no-ai) NO_AI=1 ;;
    --help|-h) sed -n 2,14p "$0"; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
  shift
done

if [ -t 1 ]; then B=$'\033[1m'; G=$'\033[32m'; Y=$'\033[33m'; R=$'\033[31m'; N=$'\033[0m'; else B=; G=; Y=; R=; N=; fi
say()  { printf '%s\n' "$*"; printf '%s\n' "$*" >> "$LOG"; }
ok()   { say "  ${G}ok${N}    $*"; }
warn() { say "  ${Y}warn${N}  $*"; WARNINGS=$((WARNINGS+1)); }
fail() { say "  ${R}FAIL${N}  $*"; say ""; say "Nothing was started. Fix the line above and run the installer again; it is safe to re-run."; exit 1; }
WARNINGS=0
ask() { # ask "question" default -> echoes answer
  if [ "$YES" = 1 ] || [ ! -t 0 ]; then echo "$2"; return; fi
  local a; read -r -p "$1 [$2]: " a || true; echo "${a:-$2}"
}
rand_alnum() { LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c "$1" || true; }
rand_b64()   { head -c 32 /dev/urandom | base64 | tr -d '\n'; }

say "${B}Hexmon IoT platform installer${N}"
say ""
say "1. Checking this machine"

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
say ""
say "2. Setup"
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
if [ -z "$ADMIN_EMAIL" ]; then ADMIN_EMAIL=$(ask "Administrator email" "admin@example.com"); fi
printf '%s' "$WORKSPACE" | grep -Eq '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$' || fail "workspace '$WORKSPACE' must be 3-40 characters: a-z, 0-9, dashes"
printf '%s' "$ADMIN_EMAIL" | grep -Eq '^[^@ ]+@[^@ ]+\.[^@ ]+$' || fail "'$ADMIN_EMAIL' is not an email address"

COMPOSE_ARGS=()
if [ "$NO_AI" = 0 ]; then
  if [ -z "$AI_MODEL" ] && [ -f model.gguf ]; then AI_MODEL=model.gguf; fi
  if [ -z "$AI_MODEL" ] && [ "$YES" = 0 ] && [ -t 0 ]; then
    AI_MODEL=$(ask "Optional local AI assistant: path to a .gguf model file (blank = skip)" "")
  fi
  if [ -n "$AI_MODEL" ]; then
    [ -f "$AI_MODEL" ] || fail "model file not found: $AI_MODEL"
    [ "$mem_mb" -ge 6144 ] || warn "the local AI needs more memory than this machine reports (${mem_mb} MB); it may be very slow"
    mkdir -p models
    [ "$(cd "$(dirname "$AI_MODEL")" && pwd)/$(basename "$AI_MODEL")" = "$(pwd)/models/model.gguf" ] || cp "$AI_MODEL" models/model.gguf
    AI_SHA=$(sha256sum models/model.gguf 2>/dev/null | cut -d' ' -f1 || shasum -a 256 models/model.gguf | cut -d' ' -f1)
    export AI_MODEL_SHA256="$AI_SHA"
    COMPOSE_ARGS=(--profile ai)
    ok "local AI model ready (sha256 ${AI_SHA:0:12}...); AI stays optional and off until enabled in Settings"
  else
    ok "no local AI model (the platform works the same without it; add one later, docs/ai-runtime.md)"
  fi
fi

# ---- install ----------------------------------------------------------------------------------
say ""
say "3. Installing (the first run takes several minutes)"
if [ "$BUNDLE" = 1 ]; then
  if [ -f SHA256SUMS ]; then
    (sha256sum -c SHA256SUMS >> "$LOG" 2>&1 || shasum -a 256 -c SHA256SUMS >> "$LOG" 2>&1) || fail "checksum mismatch: the bundle is damaged or was changed. Copy it again."
    ok "bundle checksums verified"
  fi
  gunzip -c images.tar.gz | docker load >> "$LOG" 2>&1 || fail "could not load images (see $LOG)"
  ok "images loaded"
  docker compose ${COMPOSE_ARGS[@]+"${COMPOSE_ARGS[@]}"} up -d --no-build >> "$LOG" 2>&1 || fail "docker compose up failed (see $LOG)"
else
  docker compose ${COMPOSE_ARGS[@]+"${COMPOSE_ARGS[@]}"} up -d --build >> "$LOG" 2>&1 || fail "docker compose up failed (see $LOG)"
fi
ok "services started"

http_ok() { if command -v curl >/dev/null 2>&1; then curl -fsS -m 3 "$1" >/dev/null 2>&1; else wget -q -T 3 -O /dev/null "$1" 2>/dev/null; fi; }
say "     waiting for the platform to answer ..."
up=0
for _ in $(seq 1 90); do
  if http_ok "http://localhost:${WEB_PORT}/healthz"; then up=1; break; fi
  sleep 2
done
[ "$up" = 1 ] || fail "the platform did not become healthy within 3 minutes. Run: docker compose logs api  (details in $LOG)"
ok "platform is healthy"

# ---- first workspace and administrator --------------------------------------------------------
say ""
say "4. First workspace"
CRED=install-credentials.txt
PASS=""
if docker compose exec -T api /bin/tenantctl list 2>>"$LOG" | grep -q "^${WORKSPACE} "; then
  ok "workspace '$WORKSPACE' already exists; the administrator was not changed"
else
  PASS=$(rand_alnum 20)
  if printf '%s\n' "$PASS" | docker compose exec -T api /bin/tenantctl create --id "$WORKSPACE" --name "$WORKSPACE" --admin-email "$ADMIN_EMAIL" --password-stdin >> "$LOG" 2>&1; then
    ( umask 077; printf 'Hexmon IoT platform first sign-in\nURL:       http://localhost:%s\nWorkspace: %s\nEmail:     %s\nPassword:  %s\n\nChange the password after signing in, then delete this file.\n' "$WEB_PORT" "$WORKSPACE" "$ADMIN_EMAIL" "$PASS" > "$CRED" )
    ok "workspace '$WORKSPACE' and administrator created"
  else
    fail "could not create the workspace (see $LOG)"
  fi
fi

# ---- done -------------------------------------------------------------------------------------
say ""
say "${G}${B}Installed.${N}"
say ""
say "  Open:      http://localhost:${WEB_PORT}"
say "  Workspace: ${WORKSPACE}"
say "  Email:     ${ADMIN_EMAIL}"
if [ -n "$PASS" ]; then
  say "  Password:  ${PASS}"
  say "             (also saved in ${CRED}, readable only by you; change it and delete the file)"
fi
say ""
say "  Stop:      docker compose stop        Start: docker compose start"
say "  Back up:   ./scripts/backup.sh        Logs:  docker compose logs -f api"
say "  Next:      Add device (guided scan and onboarding), Settings (email, SSO, AI)"
[ "$WARNINGS" -eq 0 ] || say "  Note:      $WARNINGS warning(s) above. The log is in $LOG."
exit 0
