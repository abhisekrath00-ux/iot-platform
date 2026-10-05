#!/usr/bin/env bash
# One-command HexThings setup for Linux and macOS (needs Docker and git or curl+tar):
#   curl -fsSL https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.sh | bash
# Downloads the source into ./hexthings (or $HEXTHINGS_DIR), then runs the guided installer with
# defaults (--yes): generated secrets, first workspace and administrator, prints the sign-in URL.
# Only the download step needs the internet; the installer builds the images on this machine.
set -euo pipefail
REPO="${HEXTHINGS_REPO:-abhisekrath00-ux/iot-platform}"; DIR="${HEXTHINGS_DIR:-hexthings}"
command -v docker >/dev/null 2>&1 || { echo "Docker is not installed. Install Docker Desktop (or docker + compose v2), then run this again."; exit 1; }
if [ -d "$DIR/.git" ]; then git -C "$DIR" pull --ff-only || true
else
  if command -v git >/dev/null 2>&1 && [ ! -e "$DIR" ]; then git clone --depth 1 "https://github.com/$REPO.git" "$DIR"
  elif command -v curl >/dev/null 2>&1 && command -v tar >/dev/null 2>&1; then
    mkdir -p "$DIR"; curl -fsSL "https://codeload.github.com/$REPO/tar.gz/refs/heads/main" | tar xz -C "$DIR" --strip-components=1
  else echo "need git, or curl and tar"; exit 1; fi
fi
cd "$DIR"
if [ -t 0 ]; then exec bash scripts/install.sh "$@"; else exec bash scripts/install.sh --yes "$@" </dev/null; fi
