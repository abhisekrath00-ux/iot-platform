#!/usr/bin/env bash
# Disposable Ubuntu Actions runner only. Real .run/systemd, no enrollment or hardware.
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] && [ "${RUNNER_OS:-}" = Linux ] || { echo 'Only run on a disposable Ubuntu Actions runner';exit 1; }
[ "$(id -u)" = 0 ] || { echo 'Run with sudo -E';exit 1; }
[ ! -e /etc/hexmon/edge-agent.yaml ] && [ ! -e /etc/systemd/system/hexmon-edge.service ] || { echo 'Existing install refused';exit 1; }
root=$(cd "$(dirname "$0")/.." && pwd)
run="$root/dist/edge/hexmon-edge-0.0.0-native-linux-amd64.run"
cleanup() { systemctl stop hexmon-edge 2>/dev/null || true;systemctl disable hexmon-edge 2>/dev/null || true; }
trap cleanup EXIT
"$run" --verify
"$run" --yes
[ "$(systemctl is-enabled hexmon-edge || true)" = disabled ]
! systemctl is-active --quiet hexmon-edge
# The unchanged example must not start or enable the service through the manager.
printf '2\n5\ny\n0\n' | /usr/local/bin/hexthings-edge
! systemctl is-active --quiet hexmon-edge
[ "$(systemctl is-enabled hexmon-edge || true)" = disabled ]
cat > /etc/hexmon/edge-agent.yaml <<'YAML'
gateway_id: ci-fixture
tenant_id: ci-fixture
serial: ci-fixture
mqtt:
  host: 127.0.0.1
  port: 18883
  tls: false
autodetect:
  enabled: false
ui:
  listen: 127.0.0.1:8088
devices: []
YAML
chown root:hexmon /etc/hexmon/edge-agent.yaml;chmod 640 /etc/hexmon/edge-agent.yaml
printf retain > /var/lib/hexmon-edge/ci-retain-marker
hash=$(sha256sum /etc/hexmon/edge-agent.yaml)
printf '5\ny\n2\n0\n' | /usr/local/bin/hexthings-edge
[ "$(systemctl is-enabled hexmon-edge)" = enabled ]
systemctl is-active --quiet hexmon-edge
ready=0
for _ in $(seq 1 20);do
 rc=0;/usr/local/bin/edge-agent -health -config /etc/hexmon/edge-agent.yaml || rc=$?
 if [ "$rc" = 0 ] || [ "$rc" = 2 ];then ready=1;break;fi;sleep 1
done
[ "$ready" = 1 ]
printf '3\n5\nn\n0\n' | /usr/local/bin/hexthings-edge
! systemctl is-active --quiet hexmon-edge
[ "$(systemctl is-enabled hexmon-edge || true)" = disabled ]
"$run" --yes
[ "$(sha256sum /etc/hexmon/edge-agent.yaml)" = "$hash" ]
[ -f /var/lib/hexmon-edge/ci-retain-marker ]
/usr/local/bin/hexthings-edge-uninstall
[ ! -e /usr/local/bin/edge-agent ]
[ ! -e /etc/systemd/system/hexmon-edge.service ]
[ "$(sha256sum /etc/hexmon/edge-agent.yaml)" = "$hash" ]
[ -f /var/lib/hexmon-edge/ci-retain-marker ]
echo 'PASS actual .run/systemd install, example gate, startup settings, start/health/stop, stopped upgrade and retained-config uninstall. No desktop/reboot/telemetry proof.'
