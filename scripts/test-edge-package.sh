#!/usr/bin/env bash
# Builds the four edge packages (scripts/build-edge.sh) and checks what the user
# downloads: installers present, Linux scripts executable in the tarball, and
# SHA256SUMS verifies. Regression: a web-based edit once dropped the exec bit
# from edge/packaging/install.sh, and the built tarball shipped `sudo ./install.sh`
# as Permission denied. Slower than the other script tests (real cross-compiles).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
fail() { echo "FAIL: $*"; exit 1; }

bash "$ROOT/scripts/build-edge.sh" 0.0.0-pkgtest >/dev/null || fail "build-edge.sh failed"
out="$ROOT/dist/edge"

[ -f "$out/SHA256SUMS" ] || fail "SHA256SUMS missing"
(cd "$out" && sha256sum -c SHA256SUMS >/dev/null) || fail "SHA256SUMS does not verify"

tgz=$(ls "$out"/hexmon-edge-0.0.0-pkgtest-linux-amd64.tar.gz 2>/dev/null) || fail "linux-amd64 tarball missing"
tar tvzf "$tgz" | grep "rwxr-xr-x .*/install.sh" >/dev/null || fail "install.sh is not executable in the tarball"
tar tvzf "$tgz" | grep "rwxr-xr-x .*/install-linux.sh" >/dev/null || fail "install-linux.sh is not executable in the tarball"
tar tvzf "$tgz" | grep "rwxr-xr-x .*/edge-agent" >/dev/null || fail "edge-agent is not executable in the tarball"

for z in windows-amd64 windows-arm64; do
  zip="$out/hexmon-edge-0.0.0-pkgtest-$z.zip"
  [ -f "$zip" ] || fail "$z zip missing"
  unzip -l "$zip" | grep "install.ps1" >/dev/null || fail "$z zip has no install.ps1"
  unzip -l "$zip" | grep "install.bat" >/dev/null || fail "$z zip has no install.bat"
  unzip -l "$zip" | grep "edge-agent.exe" >/dev/null || fail "$z zip has no edge-agent.exe"
done

echo "edge package checks passed"
