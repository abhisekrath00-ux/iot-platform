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
# Experimental staging-only paths run the bundled agent directly, before any
# installer/user/systemd/config mutation. JSON stdin supplies an explicit new
# private staging directory and pinned trust. No installed identity is accepted
# or copied into place, and no pending state is cleared by these paths.
for staging_arg in "$@"; do
  case "$staging_arg" in
    --stage-retained-claim|--recover-staged-claim|--review-staged-claim|--review-staged-install|--create-staged-install|--prepare-private-install|--review-private-install|--accept-private-identity|--review-runtime-mapping|--assemble-private-runtime)
      [ "$#" = 1 ] || { echo 'Staged enrollment cannot be combined with installer arguments.' >&2;exit 2; };;
  esac
done
case "${1:-}" in
  --stage-retained-claim|--recover-staged-claim|--review-staged-claim|--review-staged-install|--create-staged-install|--prepare-private-install|--review-private-install|--accept-private-identity|--review-runtime-mapping|--assemble-private-runtime)
    [ "$#" = 1 ] || { echo 'Staged enrollment cannot be combined with installer arguments.' >&2;exit 2; }
    here="$(cd "$(dirname "$0")" && pwd)"
    case "$(uname -m)" in
      x86_64|amd64) staged_bin="$here/edge-agent-linux-amd64";;
      aarch64|arm64) staged_bin="$here/edge-agent-linux-arm64";;
      *) echo 'Unsupported staging architecture.' >&2;exit 2;;
    esac
    [ -f "$staged_bin" ] || staged_bin="$here/edge-agent"
    [ -f "$staged_bin" ] && [ -x "$staged_bin" ] && [ ! -L "$staged_bin" ] || { echo 'Exact bundled staging executable missing or unsafe.' >&2;exit 2; }
    case "$1" in
      --stage-retained-claim) exec "$staged_bin" -create-retained-claim;;
      --recover-staged-claim) exec "$staged_bin" -recover-retained-claim;;
      --assemble-private-runtime) exec "$staged_bin" -assemble-private-runtime;;
      --review-runtime-mapping) exec "$staged_bin" -review-runtime-mapping;;
      --accept-private-identity) exec "$staged_bin" -accept-private-identity;;
      --review-private-install) exec "$staged_bin" -review-private-install;;
      --prepare-private-install) exec "$staged_bin" -prepare-private-install;;
      --create-staged-install) exec "$staged_bin" -create-staged-install;;
      --review-staged-install) exec "$staged_bin" -review-staged-install;;
      --review-staged-claim) exec "$staged_bin" -review-retained-claim;;
    esac
    ;;
esac
PREFIX="${HEXMON_PREFIX:-/usr/local/bin}"
ETC="${HEXMON_ETC:-/etc/hexmon}"
STATE="${HEXMON_STATE:-/var/lib/hexmon-edge}"
UNIT_DIR="${HEXMON_UNIT_DIR:-/etc/systemd/system}"
SKIP_SYSTEMD="${HEXMON_SKIP_SYSTEMD:-0}"
SKIP_USER="${HEXMON_SKIP_USER:-0}"

# Never turn a staged/uncertain result into an installed identity by rerunning
# ordinary setup. Existing/partial identity also forbids legacy enrollment.
# These checks precede user, directory, binary, config or unit mutations.
for retained_name in candidate-started candidate-intent.json candidate-response.json request-started intent.json response.json; do
  if [ -e "$STATE/$retained_name" ] || [ -L "$STATE/$retained_name" ]; then
    echo 'Retained enrollment requires explicit staged review/recovery; installation remains blocked.' >&2;exit 2
  fi
done
for enrollment_arg in "$@"; do
  case "$enrollment_arg" in
    -claim-code|-claim-code=*|-claim-api|-claim-api=*|-claim-serial|-claim-serial=*|-enroll|-enroll=*)
      for identity_name in identity.key identity.json identity.crt ca.pem device-ca.pem; do
        if [ -e "$STATE/$identity_name" ] || [ -L "$STATE/$identity_name" ]; then
          echo 'Existing or partial identity refuses new enrollment; preserve it and use explicit recovery.' >&2;exit 2
        fi
      done;;
  esac
done

[ "$(id -u)" = 0 ] || [ "$SKIP_SYSTEMD" = 1 ] || { echo "run as root (sudo)"; exit 1; }
here="$(cd "$(dirname "$0")" && pwd)"
if [ "$PREFIX" = /usr/local/bin ] && [ -x /usr/bin/edge-agent ] && command -v dpkg-query >/dev/null && dpkg-query -W -f='${Status}' hexmon-edge 2>/dev/null | grep -q 'install ok installed'; then
  echo 'The Debian package is installed. Upgrade with .deb; do not mix run/archive installation.' >&2;exit 1
fi
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
  id hexmon >/dev/null 2>&1 || useradd --system --user-group --home "$STATE" --shell /usr/sbin/nologin hexmon
  usermod -aG dialout hexmon 2>/dev/null || true   # serial port access
fi
install -d -m 750 "$STATE" ; [ "$SKIP_USER" = 1 ] || chown hexmon:hexmon "$STATE"
install -d -m 755 "$ETC"
install -d -m 755 "$PREFIX"

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

if [ -f "$here/edge-manager.sh" ]; then install -m 755 "$here/edge-manager.sh" "$PREFIX/hexthings-edge"; fi
if [ -f "$here/uninstall-linux.sh" ]; then install -m 755 "$here/uninstall-linux.sh" "$PREFIX/hexthings-edge-uninstall"; fi
install -m 640 "$here/edge-agent.example.yaml" "$ETC/edge-agent.example.yaml"
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
# The installed app opens a terminal operator menu, not an agent tied to a user login.
if [ "$PREFIX" = /usr/local/bin ]; then
  install -d /usr/share/applications
  cat > /usr/share/applications/hexthings-edge.desktop <<DESKTOP
[Desktop Entry]
Type=Application
Name=HexThings Edge
Comment=Configure and manage the HexThings edge service
Exec=sudo /usr/local/bin/hexthings-edge
Terminal=true
Icon=applications-internet
Categories=System;Network;
DESKTOP
fi
systemctl daemon-reload
# Fresh installs are Manual until the operator finishes configuration and chooses boot startup.
if [ "$upgrading" = 0 ]; then systemctl disable hexmon-edge >/dev/null; fi

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
