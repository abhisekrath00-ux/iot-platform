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

# Separate, disposable synthetic Compose acceptance. No owner network or config.
python3 "$root/scripts/test-broker-supervisor.py"
python3 "$root/scripts/test-secure-transaction.py"
python3 "$root/scripts/test-secure-application.py"
python3 "$root/scripts/test-secure-application-docker.py"
python3 "$root/scripts/test-secure-application-reads.py"
python3 "$root/scripts/test-secure-application-client.py"
python3 "$root/scripts/test-secure-application-acceptance.py"
python3 "$root/scripts/test-secure-application-writes.py"
python3 "$root/scripts/test-secure-disposable-docker.py"
python3 "$root/scripts/test-secure-transport-docker.py"
python3 "$root/scripts/test-secure-pairing-native-guards.py"
python3 "$root/scripts/test-secure-source-native-guards.py"
python3 "$root/scripts/test-secure-readiness.py"
python3 "$root/scripts/test-secure-runtime.py"
python3 "$root/scripts/test-secure-state.py"
python3 "$root/scripts/test-secure-recovery-review.py"
python3 "$root/scripts/test-secure-update-review.py"
python3 "$root/scripts/test-secure-execution.py"
python3 "$root/scripts/test-secure-postgres-fence.py"
python3 "$root/scripts/test-secure-postgres-hosttool.py"
python3 "$root/scripts/test-secure-postgres-namespace.py"
python3 "$root/scripts/test-secure-postgres-outside.py"
python3 "$root/scripts/test-secure-postgres-hba.py"
python3 "$root/scripts/test-secure-postgres-hba-docker.py"
python3 "$root/scripts/test-secure-postgres-operator.py"
python3 "$root/scripts/test-secure-application-startgate.py"
python3 "$root/scripts/test-secure-application-startgroup.py"
python3 "$root/scripts/test-secure-application-provider.py"
python3 "$root/scripts/test-secure-application-local-admission.py"
python3 "$root/scripts/test-secure-application-connections.py"
python3 "$root/scripts/test-secure-application-production.py"
python3 "$root/scripts/test-secure-preflight.py"
python3 "$root/scripts/test-secure-setup.py"
python3 "$root/scripts/test-secure-native-linux.py"

# Separate application-only old/new image acceptance, synthetic fixture only.
python3 "$root/scripts/test-secure-application-native.py"
