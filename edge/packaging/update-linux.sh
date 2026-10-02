#!/usr/bin/env bash
# Verifies a downloaded edge package against SHA256SUMS, then upgrades in place
# using the package's own installer (with automatic rollback).
#   sudo ./update-linux.sh hexmon-edge-<ver>-linux-<arch>.tar.gz [SHA256SUMS]
set -euo pipefail
pkg="${1:?usage: update-linux.sh PACKAGE.tar.gz [SHA256SUMS]}"
sums="${2:-$(dirname "$pkg")/SHA256SUMS}"
[ -f "$sums" ] || { echo "no SHA256SUMS next to the package; pass its path as the 2nd argument"; exit 1; }
name="$(basename "$pkg")"
want="$(awk -v n="$name" '$2==n || $2=="*"n {print $1}' "$sums")"
[ -n "$want" ] || { echo "$name is not listed in $sums"; exit 1; }
got="$(sha256sum "$pkg" | awk '{print $1}')"
[ "$want" = "$got" ] || { echo "checksum mismatch for $name: refusing to install"; exit 1; }
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
tar -xzf "$pkg" -C "$tmp"
dir="$(find "$tmp" -maxdepth 1 -mindepth 1 -type d | head -1)"
"$dir/install-linux.sh"
