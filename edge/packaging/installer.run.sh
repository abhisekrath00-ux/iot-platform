#!/usr/bin/env bash
# Self-contained HexThings Linux installer. The payload follows the marker.
set -euo pipefail
if [ "${1:-}" = --help ]; then
 echo 'HexThings Edge executable installer. Run: chmod +x FILE.run; sudo ./FILE.run'
 echo 'Asks server/claim/serial, optional config and boot startup. No public ports opened.'
 exit 0
fi
if [ "${1:-}" = --verify ]; then
 line=$(awk '/^__HEXTHINGS_PAYLOAD__$/ {print NR+1; exit}' "$0")
 [ -n "$line" ] || exit 1
 tail -n +"$line" "$0" | tar tzf - >/dev/null
 echo 'Bundled archive CRC/layout readable. This is not a signature or installation test.'
 exit 0
fi
staging=0
for staging_arg in "$@"; do
 case "$staging_arg" in
  --stage-retained-claim|--recover-staged-claim|--review-staged-claim|--review-staged-install|--create-staged-install|--prepare-private-install|--review-private-install|--accept-private-identity|--review-runtime-mapping|--assemble-private-runtime)
   [ "$#" = 1 ] || { echo 'Staged enrollment cannot be combined with installer arguments.' >&2;exit 2; }
   staging=1;;
 esac
done
# Staging changes only its explicit private retained attempt, never installation.
[ "$staging" = 1 ] || [ "$(id -u)" = 0 ] || { echo 'Run this installer with sudo.' >&2; exit 1; }
tmp=$(mktemp -d);trap 'rm -rf "$tmp"' EXIT
line=$(awk '/^__HEXTHINGS_PAYLOAD__$/ {print NR+1; exit}' "$0")
[ -n "$line" ] || { echo 'Missing bundled installer payload' >&2;exit 1; }
tail -n +"$line" "$0" | tar xzf - -C "$tmp" --no-same-owner
pkg=$(find "$tmp" -mindepth 1 -maxdepth 1 -type d)
[ -f "$pkg/install.sh" ] || { echo 'Invalid bundled installer' >&2; exit 1; }
"$pkg/install.sh" "$@"
exit $?
__HEXTHINGS_PAYLOAD__
