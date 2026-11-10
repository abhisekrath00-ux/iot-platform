#!/usr/bin/env bash
# Local operator menu installed by the executable installer. All writes are explicit.
set -euo pipefail
BIN=${HEXMON_PREFIX:-/usr/local/bin}/edge-agent
CONFIG=${HEXMON_ETC:-/etc/hexmon}/edge-agent.yaml
STATE=${HEXMON_STATE:-/var/lib/hexmon-edge}
METHOD=tar
PREF=${HEXMON_ETC:-/etc/hexmon}/start-on-boot
[ "$(id -u)" = 0 ] || { echo 'Run sudo hexthings-edge';exit 1; }
ready() {
 [ -f "$CONFIG" ] || { echo 'Import generated gateway YAML first.';return 1; }
 if [ -f "${HEXMON_ETC:-/etc/hexmon}/edge-agent.example.yaml" ] && cmp -s "$CONFIG" "${HEXMON_ETC:-/etc/hexmon}/edge-agent.example.yaml"; then echo 'Example config is not ready. Import generated gateway YAML.';return 1;fi
 sudo -u hexmon "$BIN" -check-config -config "$CONFIG" -identity-dir "$STATE"
}
while true;do
 echo;echo 'HexThings Edge: 1 Import config | 2 Start | 3 Stop | 4 Health | 5 Boot startup | 6 Logs | 7 Uninstall | 0 Exit'
 read -r -p 'Choose: ' choice
 case "$choice" in
  1) read -r -p 'Path to generated gateway YAML: ' file
     if systemctl is-active --quiet hexmon-edge;then echo 'Stop the service before importing config.';continue;fi
     if ! "$BIN" -check-config -config "$file" -identity-dir "$STATE";then echo 'Config refused.';continue;fi
     if cmp -s "$file" "${HEXMON_ETC:-/etc/hexmon}/edge-agent.example.yaml";then echo 'Unchanged example refused.';continue;fi
     [ ! -f "$CONFIG" ] || cp -p "$CONFIG" "$CONFIG.previous"
     install -m 640 -o root -g hexmon "$file" "$CONFIG";if ready && [ -f "$PREF" ];then systemctl enable hexmon-edge;fi;echo 'Imported. Choose boot startup and Start, then verify readings in dashboard.';;
  2) if ready;then if [ -f "$PREF" ];then systemctl enable hexmon-edge;fi;systemctl start hexmon-edge;systemctl is-active hexmon-edge;echo 'Service started, not proof of telemetry. Check health and sensor readings.';fi;;
  3) systemctl stop hexmon-edge;echo 'Stopped. Boot choice unchanged.';;
  4) rc=0;"$BIN" -health -config "$CONFIG" || rc=$?;case "$rc" in 0) echo 'Local health OK. Check first sensor reading.';;2) echo 'Running but broker unreachable; readings may be buffered.';;*) echo "Not healthy/running (exit $rc).";;esac;;
  5) read -r -p 'Start automatically on PC boot? [y/N]: ' boot
     if [[ "$boot" =~ ^[Yy]$ ]];then if ready;then printf "yes\n" > "$PREF";chmod 640 "$PREF";systemctl enable hexmon-edge;fi;else rm -f "$PREF";systemctl disable hexmon-edge;fi;;
  6) journalctl -u hexmon-edge -n 80 --no-pager;;
  7) read -r -p 'Remove edge service/executable, keeping config and identity? Type REMOVE EDGE: ' answer
     if [ "$answer" = 'REMOVE EDGE' ];then if [ "$METHOD" = deb ];then dpkg -r hexmon-edge;else "${HEXMON_PREFIX:-/usr/local/bin}/hexthings-edge-uninstall";fi;exit $?;fi;;
  0) exit 0;;*) echo 'Choose a listed number.';;
 esac
done
