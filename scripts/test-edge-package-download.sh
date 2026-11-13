#!/usr/bin/env bash
# Build ten real packages and read them through the authenticated API handler.
# No install, sudo, service changes, owner enrollment, browser save or release.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
version=${1:-0.0.0-download-fixture}
bash "$root/scripts/build-edge.sh" "$version"
# No silent opt-in test skip: these are absolute paths, since go test changes cwd.
export TEST_EDGE_PACKAGE_DIR="$root/dist/edge" TEST_EDGE_PACKAGE_VERSION="$version"
(cd "$root/server" && go test ./cmd/api -run '^TestRealBuiltEdgePackagesDownload$' -count=1 -v)
# Read-only execution of the freshly packaged native Linux agent, not install.
if [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ]; then
 scratch=$(mktemp -d);trap 'rm -rf "$scratch"' EXIT
 tar -xzf "$root/dist/edge/hexmon-edge-$version-linux-amd64.tar.gz" -C "$scratch"
 result=$("$scratch/hexmon-edge-$version-linux-amd64/edge-agent" -version)
 [ "$result" = "hexmon-edge-agent $version linux/amd64" ] || { echo 'Packaged agent version mismatch';exit 1; }
 printf '%s\n' "$result"
fi
echo 'Real package download contract passed; not browser/native installer or hardware acceptance.'
