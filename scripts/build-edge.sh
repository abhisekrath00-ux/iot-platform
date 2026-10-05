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
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$stage/edge-agent$ext" ./cmd/edge-agent
  cp packaging/edge-agent.example.yaml "$stage/"
  if [ "$os" = linux ]; then cp packaging/install-linux.sh packaging/install.sh packaging/uninstall-linux.sh packaging/update-linux.sh "$stage/"; else cp packaging/install-windows.ps1 packaging/install.ps1 packaging/install.bat packaging/uninstall-windows.ps1 packaging/update-windows.ps1 "$stage/"; fi
  if [ "$os" = linux ] && command -v dpkg-deb >/dev/null; then
    deb="$out/hexmon-edge_${ver#v}_$arch.deb"; d="$(mktemp -d)"
    install -d "$d/DEBIAN" "$d/usr/bin" "$d/etc/hexmon" "$d/lib/systemd/system"
    install -m 755 "$stage/edge-agent" "$d/usr/bin/edge-agent"
    install -m 640 packaging/edge-agent.example.yaml "$d/etc/hexmon/edge-agent.yaml"
    install -m 644 packaging/hexmon-edge.service "$d/lib/systemd/system/hexmon-edge.service"
    sed "s/@VERSION@/${ver#v}/; s/@ARCH@/$arch/" packaging/deb/control > "$d/DEBIAN/control"
    cp packaging/deb/conffiles packaging/deb/postinst packaging/deb/prerm packaging/deb/postrm "$d/DEBIAN/"
    chmod 755 "$d/DEBIAN/postinst" "$d/DEBIAN/prerm" "$d/DEBIAN/postrm"
    dpkg-deb --root-owner-group -Zgzip --build "$d" "$deb" >/dev/null; rm -rf "$d"
  fi
  if [ "$os" = linux ]; then tar -C "$out" -czf "$out/$name.tar.gz" "$name"; else (cd "$out" && zip -qr "$name.zip" "$name"); fi
  rm -rf "$stage"
done
(cd "$out" && sha256sum *.tar.gz *.zip *.deb > SHA256SUMS)
ls -la "$out"
