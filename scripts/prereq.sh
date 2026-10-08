#!/usr/bin/env bash
# HexThings prerequisite check and installer for Linux, macOS and WSL (single node).
#   bash scripts/prereq.sh [--yes] [--check]
# Checks: tools to download (curl+tar or git), Docker, Docker Compose v2, a running Docker engine, RAM, disk.
# If something is missing it prints the exact plan, asks ONCE, then installs only that. Nothing else on the
# machine is touched. --check only reports. --yes (or HEXTHINGS_YES=1) accepts the plan.
# Exit codes: 0 ready, 1 failed (reason printed), 2 you declined or it cannot ask, 3 sign-out/in needed for the docker group.
# Needs the internet only for the install step; an air-gapped site uses the bundle installer instead.
# Test hooks: HEXTHINGS_FAKE_OS (linux|darwin), HEXTHINGS_FAKE_MEM_MB, HEXTHINGS_FAKE_DISK_MB, HEXTHINGS_FAKE_ROOT=1.
set -uo pipefail
YES="${HEXTHINGS_YES:-0}"; CHECK=0
for a in "$@"; do case "$a" in --yes|-y) YES=1 ;; --check) CHECK=1 ;; -h|--help) sed -n 2,10p "$0"; exit 0 ;; esac; done
OS="${HEXTHINGS_FAKE_OS:-$(uname -s | tr A-Z a-z)}"
say() { printf '%s\n' "$*"; }
ok() { say "  ok    $*"; }
warn() { say "  warn  $*"; }
bad() { say "  FAIL  $*"; }
is_root() { [ "${HEXTHINGS_FAKE_ROOT:-0}" = 1 ] || [ "$(id -u)" = 0 ]; }
SUDO=""; is_root || SUDO="sudo"

say "HexThings prerequisite check ($OS)"
plan=(); fail=0

# --- facts ---------------------------------------------------------------------------------------------
mem=${HEXTHINGS_FAKE_MEM_MB:-}
if [ -z "$mem" ]; then
  if [ -r /proc/meminfo ]; then mem=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
  elif command -v sysctl >/dev/null 2>&1; then mem=$(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1048576 )); else mem=0; fi
fi
disk=${HEXTHINGS_FAKE_DISK_MB:-$(df -Pm . 2>/dev/null | awk 'NR==2 {print $4}')}
if [ "${mem:-0}" -ge 4000 ] 2>/dev/null; then ok "memory ${mem} MB"; elif [ "${mem:-0}" -gt 0 ] 2>/dev/null; then warn "memory ${mem} MB; about 4 GB is recommended, it may be slow"; fi
if [ "${disk:-0}" -ge 10000 ] 2>/dev/null; then ok "free disk ${disk} MB"
elif [ "${disk:-0}" -ge 5000 ] 2>/dev/null; then warn "free disk ${disk} MB; about 10 GB is recommended"
elif [ -n "${disk:-}" ]; then bad "free disk ${disk} MB; at least 5 GB is needed. Free some space and run this again."; fail=1; fi

have_dl=0; { command -v git >/dev/null 2>&1 || { command -v curl >/dev/null 2>&1 && command -v tar >/dev/null 2>&1; }; } && have_dl=1
if [ $have_dl = 1 ]; then ok "download tools (git, or curl and tar)"; else bad "need git, or curl and tar, to download. Install them with your package manager (for example: sudo apt-get install -y curl tar) and run this again."; fail=1; fi

has_docker=0; engine=0; compose=0
command -v docker >/dev/null 2>&1 && has_docker=1
[ $has_docker = 1 ] && docker info >/dev/null 2>&1 && engine=1
[ $has_docker = 1 ] && docker compose version >/dev/null 2>&1 && compose=1
if [ $has_docker = 0 ]; then warn "Docker is not installed"
elif [ $compose = 0 ]; then warn "Docker is installed but Compose v2 is missing ('docker compose version' fails)"
elif [ $engine = 0 ]; then warn "Docker is installed but the engine is not running (or this user may not use it)"
else ok "Docker and Compose v2 are working"; fi

# --- plan ----------------------------------------------------------------------------------------------
STARTCMD=""
if [ $fail = 0 ] && { [ $has_docker = 0 ] || [ $compose = 0 ]; }; then
  case "$OS" in
    linux) plan+=("Install Docker Engine and the Compose plugin with Docker's official script (https://get.docker.com), using ${SUDO:-root rights}")
           plan+=("Start and enable the docker service")
           id -nG 2>/dev/null | tr ' ' '\n' | grep -qx docker || is_root || plan+=("Add user '$(id -un)' to the 'docker' group (so you can use Docker without sudo)") ;;
    darwin) if command -v brew >/dev/null 2>&1; then plan+=("Install Docker Desktop with Homebrew: brew install --cask docker")
            else bad "Docker Desktop is not installed and Homebrew is not available. Download Docker Desktop from https://www.docker.com/products/docker-desktop/ , start it once, then run this again."; fail=1; fi ;;
    *) bad "unsupported system '$OS'. Use Linux, macOS, or Windows (get.ps1)."; fail=1 ;;
  esac
elif [ $fail = 0 ] && [ $engine = 0 ]; then
  case "$OS" in
    linux) plan+=("Start the docker service (${SUDO:-as root} systemctl start docker)") ;;
    darwin) plan+=("Start Docker Desktop (open -a Docker) and wait for it") ;;
  esac
fi

if [ $fail = 1 ]; then say ""; say "Not ready. Fix the items marked FAIL above."; exit 1; fi
if [ ${#plan[@]} -eq 0 ]; then say ""; say "Everything needed is present."; exit 0; fi
if [ $CHECK = 1 ]; then say ""; say "Would do:"; printf '  - %s\n' "${plan[@]}"; exit 2; fi

say ""; say "To continue, this will change the machine:"
printf '  - %s\n' "${plan[@]}"
say "Nothing else is touched. It needs the internet for the download."
if [ "$YES" != 1 ]; then
  if [ -t 0 ]; then r=""; read -r -p "Go ahead? [y/N] " r
  elif ( : < /dev/tty ) 2>/dev/null; then r=""; read -r -p "Go ahead? [y/N] " r < /dev/tty
  else say "Cannot ask here (no terminal). Run again with --yes (or HEXTHINGS_YES=1) to accept the plan above."; exit 2; fi
  case "$r" in y|Y|yes|YES) ;; *) say "Declined. Nothing was changed."; exit 2 ;; esac
fi
if [ -n "$SUDO" ] && ! command -v sudo >/dev/null 2>&1; then bad "this needs root rights and 'sudo' is not installed. Run as root or install sudo."; exit 1; fi
if [ $has_docker = 0 ] || [ $compose = 0 ]; then
  if [ "$OS" = linux ]; then
    curl -fsSL --max-time 20 -o /dev/null https://get.docker.com 2>/dev/null || { bad "cannot reach https://get.docker.com (offline or blocked). On an air-gapped site use the bundle installer (docs/airgap.md)."; exit 1; }
    tmp=$(mktemp); curl -fsSL https://get.docker.com -o "$tmp" || { bad "download of Docker's install script failed"; exit 1; }
    $SUDO sh "$tmp" || { rm -f "$tmp"; bad "Docker's install script failed (see its output above; unsupported distribution?). Install Docker by hand: https://docs.docker.com/engine/install/"; exit 1; }
    rm -f "$tmp"
    command -v systemctl >/dev/null 2>&1 && $SUDO systemctl enable --now docker >/dev/null 2>&1
    if ! is_root && ! id -nG | tr ' ' '\n' | grep -qx docker; then $SUDO usermod -aG docker "$(id -un)" && ADDED=1; fi
  else
    brew install --cask docker || { bad "brew install failed"; exit 1; }
  fi
fi
if ! docker info >/dev/null 2>&1; then
  case "$OS" in
    linux) command -v systemctl >/dev/null 2>&1 && $SUDO systemctl start docker >/dev/null 2>&1 ;;
    darwin) open -a Docker >/dev/null 2>&1 ;;
  esac
  say "Waiting for the Docker engine (up to 3 minutes)..."
  for _ in $(seq 1 ${HEXTHINGS_WAIT_TRIES:-90}); do docker info >/dev/null 2>&1 && break; sleep 2; done
fi
if docker info >/dev/null 2>&1; then ok "Docker engine is running"; docker compose version >/dev/null 2>&1 || { bad "Compose v2 still missing"; exit 1; }; exit 0; fi
if [ "$OS" = linux ] && [ -n "$SUDO" ] && $SUDO docker info >/dev/null 2>&1; then
  say "  Docker works, but this login is not in the 'docker' group yet."; exit 3
fi
bad "Docker is installed but the engine did not start. Look at: ${SUDO:+sudo }systemctl status docker (Linux) or open Docker Desktop and accept its terms (macOS). Then run this again."
exit 1
