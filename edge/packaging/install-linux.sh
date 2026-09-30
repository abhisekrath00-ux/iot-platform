#!/usr/bin/env bash
# Installs the Hexmon edge agent as a systemd service (Ubuntu/Debian/any systemd distro).
# Usage: sudo ./install-linux.sh [-claim-api URL -claim-code CODE -claim-serial SERIAL]
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "run as root (sudo)"; exit 1; }
here="$(cd "$(dirname "$0")" && pwd)"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) bin=edge-agent-linux-amd64 ;;
  aarch64|arm64) bin=edge-agent-linux-arm64 ;;
  *) echo "unsupported arch $arch"; exit 1 ;;
esac
[ -f "$here/$bin" ] || bin=edge-agent   # single-arch package
id hexmon >/dev/null 2>&1 || useradd --system --home /var/lib/hexmon-edge --shell /usr/sbin/nologin hexmon
usermod -aG dialout hexmon 2>/dev/null || true   # serial port access
install -d -o hexmon -g hexmon -m 750 /var/lib/hexmon-edge
install -d -m 755 /etc/hexmon
install -m 755 "$here/$bin" /usr/local/bin/edge-agent
[ -f /etc/hexmon/edge-agent.yaml ] || install -m 640 -o root -g hexmon "$here/edge-agent.example.yaml" /etc/hexmon/edge-agent.yaml
if [ "${1:-}" = "-claim-code" ] || [ "${1:-}" = "-claim-api" ]; then
  sudo -u hexmon /usr/local/bin/edge-agent "$@"
fi
cat > /etc/systemd/system/hexmon-edge.service <<UNIT
[Unit]
Description=Hexmon edge agent
After=network-online.target
Wants=network-online.target
[Service]
User=hexmon
Group=hexmon
ExecStart=/usr/local/bin/edge-agent
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/hexmon-edge /etc/hexmon
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable hexmon-edge
echo "Installed. Edit /etc/hexmon/edge-agent.yaml (or claim), then: systemctl start hexmon-edge && journalctl -u hexmon-edge -f"
