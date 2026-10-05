#!/usr/bin/env bash
# Installs or upgrades the HexThings edge agent as a systemd service
# (Ubuntu, Debian, Raspberry Pi OS, any systemd distro; x86_64 and arm64).
#
#   sudo ./install-linux.sh [-claim-api URL -claim-code CODE -claim-serial SERIAL]
#
# Re-running is the upgrade path: the old binary is kept as edge-agent.prev, the
# config and identity are never touched, and if the new version does not report
# healthy within 60 seconds the old binary is put back.
#
# Test/packaging overrides (all default to the real system paths):
#   HEXMON_PREFIX HEXMON_ETC HEXMON_STATE HEXMON_UNIT_DIR HEXMON_SKIP_SYSTEMD=1 HEXMON_SKIP_USER=1
set -euo pipefail
PREFIX="${HEXMON_PREFIX:-/usr/local/bin}"
ETC="${HEXMON_ETC:-/etc/hexmon}"
STATE="${HEXMON_STATE:-/var/lib/hexmon-edge}"
UNIT_DIR="${HEXMON_UNIT_DIR:-/etc/systemd/system}"
SKIP_SYSTEMD="${HEXMON_SKIP_SYSTEMD:-0}"
SKIP_USER="${HEXMON_SKIP_USER:-0}"

[ "$(id -u)" = 0 ] || [ "$SKIP_SYSTEMD" = 1 ] || { echo "run as root (sudo)"; exit 1; }
here="$(cd "$(dirname "$0")" && pwd)"
case "$(uname -m)" in
  x86_64|amd64) bin=edge-agent-linux-amd64 ;;
  aarch64|arm64) bin=edge-agent-linux-arm64 ;;
  *) echo "unsupported architecture $(uname -m): need x86_64 or arm64"; exit 1 ;;
esac
[ -f "$here/$bin" ] || bin=edge-agent   # single-arch package
[ -f "$here/$bin" ] || { echo "binary not found next to this script"; exit 1; }
new_ver="$("$here/$bin" -version 2>/dev/null || echo unknown)"

# The binary must at least start on this CPU before we touch anything.
"$here/$bin" -version >/dev/null || { echo "this binary does not run on this machine"; exit 1; }

if [ "$SKIP_USER" != 1 ]; then
  id hexmon >/dev/null 2>&1 || useradd --system --home "$STATE" --shell /usr/sbin/nologin hexmon
  usermod -aG dialout hexmon 2>/dev/null || true   # serial port access
fi
install -d -m 750 "$STATE" ; [ "$SKIP_USER" = 1 ] || chown hexmon:hexmon "$STATE"
install -d -m 755 "$ETC"

upgrading=0
if [ -x "$PREFIX/edge-agent" ]; then
  upgrading=1
  echo "upgrading $("$PREFIX/edge-agent" -version 2>/dev/null || echo unknown) -> $new_ver"
  cp -p "$PREFIX/edge-agent" "$PREFIX/edge-agent.prev"
fi
install -m 755 "$here/$bin" "$PREFIX/edge-agent.new"
mv -f "$PREFIX/edge-agent.new" "$PREFIX/edge-agent"

if [ ! -f "$ETC/edge-agent.yaml" ]; then
  install -m 640 "$here/edge-agent.example.yaml" "$ETC/edge-agent.yaml"
  [ "$SKIP_USER" = 1 ] || chown root:hexmon "$ETC/edge-agent.yaml"
fi

case "${1:-}" in
  -claim-code|-claim-api|-enroll)
    if [ "$SKIP_USER" = 1 ]; then "$PREFIX/edge-agent" -identity-dir "$STATE" "$@"
    else sudo -u hexmon "$PREFIX/edge-agent" -identity-dir "$STATE" "$@"; fi ;;
esac

if [ "$SKIP_SYSTEMD" = 1 ]; then echo "installed to $PREFIX (systemd step skipped)"; exit 0; fi

cat > "$UNIT_DIR/hexmon-edge.service" <<UNIT
[Unit]
Description=HexThings edge agent
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=10
[Service]
User=hexmon
Group=hexmon
SupplementaryGroups=dialout
ExecStartPre=$PREFIX/edge-agent -check-config -config $ETC/edge-agent.yaml -identity-dir $STATE
ExecStart=$PREFIX/edge-agent -config $ETC/edge-agent.yaml -identity-dir $STATE
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$STATE $ETC
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable hexmon-edge >/dev/null

rollback() {
  echo "new version is not healthy; restoring the previous binary"
  if [ "$upgrading" = 1 ] && [ -f "$PREFIX/edge-agent.prev" ]; then
    mv -f "$PREFIX/edge-agent.prev" "$PREFIX/edge-agent"
    systemctl restart hexmon-edge || true
  fi
  exit 1
}

if [ "$upgrading" = 1 ]; then
  # Only an already-running service is restarted. A fresh install waits for
  # the operator to finish the config.
  if systemctl is-active --quiet hexmon-edge; then
    systemctl restart hexmon-edge
    ok=0
    for _ in $(seq 1 30); do
      sleep 2
      # exit 0 healthy, 2 running but broker unreachable (not the upgrade's fault)
      rc=0; "$PREFIX/edge-agent" -health -config "$ETC/edge-agent.yaml" >/dev/null 2>&1 || rc=$?
      if [ "$rc" = 0 ] || [ "$rc" = 2 ]; then ok=1; break; fi
    done
    [ "$ok" = 1 ] || rollback
    echo "upgrade complete; previous binary kept as $PREFIX/edge-agent.prev"
  else
    echo "upgrade installed; service was not running, start it with: systemctl start hexmon-edge"
  fi
else
  echo "Installed. Check the config: $PREFIX/edge-agent -check-config -config $ETC/edge-agent.yaml"
  echo "Then: systemctl start hexmon-edge && journalctl -u hexmon-edge -f"
  echo "Serial ports seen by this machine: $PREFIX/edge-agent -list-ports"
fi
