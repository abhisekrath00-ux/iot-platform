#!/usr/bin/env bash
# One-command HexThings setup for Linux and macOS. Checks what is needed (Docker, Compose v2, a running engine,
# RAM, disk), installs ONLY what is missing after one yes/no question, then installs and starts HexThings:
#   curl -fsSL https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.sh | bash
# Downloads the source (git, or curl+tar) into ./hexthings (or $HEXTHINGS_DIR), then runs the guided installer with
# defaults (--yes): generated secrets, first workspace and administrator, prints the sign-in URL.
# Only the download step needs the internet; the installer builds the images on this machine.
set -euo pipefail
REPO="${HEXTHINGS_REPO:-abhisekrath00-ux/iot-platform}"; DIR="${HEXTHINGS_DIR:-hexthings}"
if [ -d "$DIR/.git" ]; then git -C "$DIR" pull --ff-only || true
else
  if command -v git >/dev/null 2>&1 && [ ! -e "$DIR" ]; then git clone --depth 1 "https://github.com/$REPO.git" "$DIR"
  elif command -v curl >/dev/null 2>&1 && command -v tar >/dev/null 2>&1; then
    mkdir -p "$DIR"; curl -fsSL "https://codeload.github.com/$REPO/tar.gz/refs/heads/main" | tar xz -C "$DIR" --strip-components=1
  else echo "need git, or curl and tar"; exit 1; fi
fi
cd "$DIR"
# Prerequisites: skips whatever is present, asks once before installing anything missing.
bash scripts/prereq.sh; rc=$?
case $rc in
  0) ;;
  3) # Docker was installed and this login is not in the docker group yet: continue inside the group
     if command -v sg >/dev/null 2>&1; then USE_SG=1; else echo "Log out and in again (or run: newgrp docker), then run this command again."; exit 3; fi ;;
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
