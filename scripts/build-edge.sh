#!/usr/bin/env bash
# Cross-compiles the edge agent (pure Go, no CGO) for Linux and Windows on
# x86_64 and arm64 and packages each with installers + SHA256SUMS.
# Output: dist/edge/
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
ver="${1:-$(git -C "$root" describe --tags --always 2>/dev/null || echo dev)}"
out="$root/dist/edge"; rm -rf "$out"; mkdir -p "$out"
cd "$root/edge"
for t in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=.exe
  name="hexmon-edge-$ver-$os-$arch"; stage="$out/$name"; mkdir -p "$stage"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$stage/edge-agent$ext" ./cmd/edge-agent
  cp packaging/edge-agent.example.yaml "$stage/"
  if [ "$os" = linux ]; then cp packaging/install-linux.sh "$stage/"; else cp packaging/install-windows.ps1 "$stage/"; fi
  if [ "$os" = linux ]; then tar -C "$out" -czf "$out/$name.tar.gz" "$name"; else (cd "$out" && zip -qr "$name.zip" "$name"); fi
  rm -rf "$stage"
done
(cd "$out" && sha256sum *.tar.gz *.zip > SHA256SUMS)
ls -la "$out"
