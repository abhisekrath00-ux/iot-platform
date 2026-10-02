#!/usr/bin/env bash
# Removes the Hexmon edge agent service and binary. Config and identity are kept
# unless you pass --purge (the identity cannot be re-created without a new claim code).
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "run as root (sudo)"; exit 1; }
systemctl disable --now hexmon-edge 2>/dev/null || true
rm -f /etc/systemd/system/hexmon-edge.service /usr/local/bin/edge-agent /usr/local/bin/edge-agent.prev
systemctl daemon-reload
if [ "${1:-}" = "--purge" ]; then
  rm -rf /etc/hexmon /var/lib/hexmon-edge
  userdel hexmon 2>/dev/null || true
  echo "removed, including config, identity and the buffered queue"
else
  echo "removed. Kept /etc/hexmon and /var/lib/hexmon-edge (identity, queue). Use --purge to delete them."
fi
