#!/usr/bin/env bash
# One-command HexThings setup for Linux and macOS. Checks what is needed (Docker, Compose v2, a running engine,
# RAM, disk), installs ONLY what is missing after one yes/no question, then installs and starts HexThings:
#   bash scripts/get.sh --local-source .   (inside an authorized checkout/extracted source)
# Uses explicit local source, or authenticated Git into ./hexthings (or $HEXTHINGS_DIR), then runs the guided installer with
# defaults (--yes): generated secrets, first workspace and administrator, prints the sign-in URL.
# Local-source selection uses no network; prerequisites/images/optional AI may still need internet.
set -euo pipefail
REPO="${HEXTHINGS_REPO:-abhisekrath00-ux/iot-platform}"; DIR="${HEXTHINGS_DIR:-hexthings}"
LOCAL_SOURCE=""
if [ "${1:-}" = --local-source ]; then
  [ $# -ge 2 ] && [ -n "$2" ] || { echo "--local-source needs a checkout/extracted source directory" >&2; exit 2; }
  LOCAL_SOURCE=$2; shift 2
  DIR=$LOCAL_SOURCE
  for f in docker-compose.yml .env.example scripts/prereq.sh scripts/install.sh scripts/hexthings; do
    [ -f "$DIR/$f" ] || { echo "local source is incomplete (missing $f); no download or install attempted" >&2; exit 1; }
  done
elif [ -d "$DIR/.git" ]; then
  git -C "$DIR" pull --ff-only || { echo "Source update failed. Authenticate Git for this private repository, or use --local-source PATH to install the existing files deliberately." >&2; exit 1; }
else
  if command -v git >/dev/null 2>&1 && [ ! -e "$DIR" ]; then
    git clone --depth 1 "https://github.com/$REPO.git" "$DIR" || { echo "Source access failed. Use an authorized Git login or owner-downloaded ZIP, then run bash scripts/get.sh --local-source . inside that source folder." >&2; exit 1; }
  else echo "Use --local-source PATH for an existing checkout/extracted source, or install Git and configure authorized private-repository access." >&2; exit 1; fi
fi
cd "$DIR"
# Put a `hexthings` command on PATH (menu, status, start, stop, doctor, ai ...). Best effort: never stops the install.
hx_target="$(pwd)/scripts/hexthings"; chmod +x "$hx_target" 2>/dev/null || true
for hx_bin in /usr/local/bin "$HOME/.local/bin"; do
  mkdir -p "$hx_bin" 2>/dev/null || true
  if [ -w "$hx_bin" ] && ln -sf "$hx_target" "$hx_bin/hexthings" 2>/dev/null; then HX_LINK="$hx_bin/hexthings"; break
  elif [ "$hx_bin" = /usr/local/bin ] && command -v sudo >/dev/null 2>&1 && sudo -n ln -sf "$hx_target" "$hx_bin/hexthings" 2>/dev/null; then HX_LINK="$hx_bin/hexthings"; break; fi
done
if [ -n "${HX_LINK:-}" ]; then echo "After setup, run: hexthings   (menu, status, doctor, logs, ai; $HX_LINK)"; else echo "After setup, run: bash $(pwd)/scripts/hexthings"; fi
case "${HX_LINK:-}" in "$HOME/.local/bin/"*) case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) echo "Add $HOME/.local/bin to your PATH to use it by name." ;; esac ;; esac
# Prerequisites: skips whatever is present, asks once before installing anything missing.
rc=0; bash scripts/prereq.sh || rc=$?   # set -e: capture the code, prereq exits 3 on purpose
case $rc in
  0) ;;
  3) # Docker was installed and this login is not in the docker group yet: continue inside the group
     if command -v sg >/dev/null 2>&1 && sg docker -c 'docker info' >/dev/null 2>&1; then USE_SG=1
     else echo "Docker group access is not active in this session yet. Log out and in again (or run: newgrp docker), then run this command again. Nothing else is needed."; exit 3; fi ;;
  *) exit $rc ;;
esac
if [ -n "${HEXTHINGS_NO_AI:-}" ]; then set -- --no-ai "$@"; fi
if [ -n "${HEXTHINGS_AI_MODEL_SIZE:-}" ]; then set -- --ai-model-size "$HEXTHINGS_AI_MODEL_SIZE" "$@"; fi
if [ "${USE_SG:-0}" = 1 ]; then
  args=$(printf ' %q' "$@"); exec sg docker -c "bash scripts/install.sh$args"
fi
if [ -t 0 ]; then exec bash scripts/install.sh "$@"
elif ( : < /dev/tty ) 2>/dev/null; then exec bash scripts/install.sh "$@" < /dev/tty
else
  # Non-interactive setup never downloads AI without an explicit flag.
  exec bash scripts/install.sh --yes "$@" </dev/null
fi
