#!/usr/bin/env bash
# Cross-compiles the edge agent (pure Go, no CGO) for Linux and Windows on
# x86_64 and arm64 and packages each with installers + SHA256SUMS.
# Output: dist/edge/
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
ver="${1:-0.2.0}"
[[ "$ver" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,63}$ ]] || { echo 'invalid edge version' >&2; exit 2; }
command -v dpkg-deb >/dev/null || { echo 'dpkg-deb is required for dashboard Ubuntu packages' >&2;exit 1; }
command -v python3 >/dev/null || { echo 'python3 is required for package inventory' >&2;exit 1; }
trap 'rm -f "$root/edge/cmd/edge-setup/payload/package.zip"' EXIT
out="$root/dist/edge"; rm -rf "$out"; mkdir -p "$out"
cd "$root/edge"
for t in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=.exe
  name="hexmon-edge-$ver-$os-$arch"; stage="$out/$name"; mkdir -p "$stage"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$stage/edge-agent$ext" ./cmd/edge-agent
  cp packaging/edge-agent.example.yaml "$stage/"
  if [ "$os" = linux ]; then cp packaging/install-linux.sh packaging/install.sh packaging/uninstall-linux.sh packaging/update-linux.sh packaging/edge-manager.sh "$stage/"; else cp packaging/install-windows.ps1 packaging/install.ps1 packaging/install.bat packaging/uninstall-windows.ps1 packaging/update-windows.ps1 packaging/setup-wizard.ps1 "$stage/"; fi
  # the repo can lose exec bits (web-based edits), but the shipped installers must stay runnable
  chmod 755 "$stage"/*.sh 2>/dev/null || true
  if [ "$os" = linux ] && command -v dpkg-deb >/dev/null; then
    deb="$out/hexmon-edge_${ver#v}_$arch.deb"; d="$(mktemp -d)"
    install -d "$d/DEBIAN" "$d/usr/bin" "$d/etc/hexmon" "$d/lib/systemd/system"
    install -m 755 "$stage/edge-agent" "$d/usr/bin/edge-agent"
    sed 's|/usr/local/bin|/usr/bin|g;s/^METHOD=tar$/METHOD=deb/' packaging/edge-manager.sh > "$d/usr/bin/hexthings-edge"; chmod 755 "$d/usr/bin/hexthings-edge"
    install -m 755 packaging/uninstall-linux.sh "$d/usr/bin/hexthings-edge-uninstall"
    install -m 640 packaging/edge-agent.example.yaml "$d/etc/hexmon/edge-agent.example.yaml"
    install -m 640 packaging/edge-agent.example.yaml "$d/etc/hexmon/edge-agent.yaml"
    install -m 644 packaging/hexmon-edge.service "$d/lib/systemd/system/hexmon-edge.service"
    sed "s/@VERSION@/${ver#v}/; s/@ARCH@/$arch/" packaging/deb/control > "$d/DEBIAN/control"
    install -d "$d/usr/share/applications"
    printf '[Desktop Entry]\nType=Application\nName=HexThings Edge\nExec=sudo /usr/bin/hexthings-edge\nTerminal=true\nIcon=applications-internet\nCategories=System;Network;\n' > "$d/usr/share/applications/hexthings-edge.desktop"
    cp packaging/deb/conffiles packaging/deb/preinst packaging/deb/postinst packaging/deb/prerm packaging/deb/postrm "$d/DEBIAN/"
    chmod 755 "$d/DEBIAN/preinst" "$d/DEBIAN/postinst" "$d/DEBIAN/prerm" "$d/DEBIAN/postrm"
    dpkg-deb --root-owner-group -Zgzip --build "$d" "$deb" >/dev/null; rm -rf "$d"
  fi
  if [ "$os" = linux ]; then
    tar -C "$out" -czf "$out/$name.tar.gz" "$name"
    cat packaging/installer.run.sh "$out/$name.tar.gz" > "$out/$name.run"; chmod 755 "$out/$name.run"
  else
    (cd "$out" && zip -qr "$name.zip" "$name")
    rm -f "$root/edge/cmd/edge-setup/payload/package.zip"
    (cd "$stage" && zip -qr "$root/edge/cmd/edge-setup/payload/package.zip" .)
    CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags '-s -w' -o "$out/$name-setup.exe" ./cmd/edge-setup
  fi
  rm -rf "$stage"
done
(cd "$out" && sha256sum *.tar.gz *.zip *.deb *.run *.exe > SHA256SUMS)
python3 "$root/scripts/edge-manifest.py" "$out" "$ver"
ls -la "$out"
