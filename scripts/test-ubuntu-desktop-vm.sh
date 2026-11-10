#!/usr/bin/env bash
# Disposable public standard-runner ONLY. Real Desktop ISO, not a container/version mock.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted ]] || { echo 'Refusing outside disposable GitHub-hosted CI'; exit 2; }
[[ -c /dev/kvm ]] || { echo 'KVM unavailable: Desktop VM test cannot run on this runner'; exit 1; }
mkdir -p desktop-evidence
base=$PWD/desktop-evidence
work=$(mktemp -d)
cleanup() {
  python3 - "$base" <<'PYEND'
from pathlib import Path
import sys
try:
 from PIL import Image
 for p in Path(sys.argv[1]).glob('*.ppm'):
  Image.open(p).save(p.with_suffix('.png'));p.unlink()
except Exception: pass
PYEND
  [[ ! -f "$work/vm.pid" ]] || kill "$(cat "$work/vm.pid")" 2>/dev/null || true; [[ -z ${seed_pid:-} ]] || kill "$seed_pid" 2>/dev/null || true; }
trap cleanup EXIT
# Reclaim only unused SDKs on this disposable runner, never any user's machine.
sudo rm -rf /usr/share/dotnet /usr/local/lib/android /opt/ghc /usr/local/.ghcup /usr/share/swift
sudo apt-get update
sudo apt-get install -y --no-install-recommends qemu-system-x86 qemu-utils xorriso python3-pil
(( $(df -Pm "$work" | awk 'NR==2 {print $4}') >= 30000 )) || { echo 'Need 30GiB real host free space for Desktop ISO and sparse guest'; exit 1; }
sudo chmod a+rw /dev/kvm
ssh-keygen -q -t ed25519 -N '' -f "$work/key"
cd "$work"
iso=ubuntu-24.04.5.1-desktop-amd64.iso
curl --fail --location --retry 2 --max-time 600 -o "$iso" "https://releases.ubuntu.com/24.04/$iso"
curl --fail --location --retry 2 -o SHA256SUMS https://releases.ubuntu.com/24.04/SHA256SUMS
grep " \*${iso}$" SHA256SUMS | sha256sum -c -
# Preserve checksum evidence, not ephemeral SSH private keys.
grep " \*${iso}$" SHA256SUMS > "$base/iso-checksum.txt"
xorriso -osirrox on -indev "$iso" -extract /casper/vmlinuz vmlinuz -extract /casper/initrd initrd
mkdir seed
python3 - <<'PY'
import json,pathlib
key=pathlib.Path('key.pub').read_text().strip()
config={'autoinstall':{'version':1,'source':{'id':'ubuntu-desktop-minimal'},'locale':'en_US.UTF-8','keyboard':{'layout':'us'},'identity':{'hostname':'hex-desktop-ci','username':'hextest','realname':'HexThings Desktop CI','password':'!'},'storage':{'layout':{'name':'direct'}},'ssh':{'install-server':True,'allow-pw':False,'authorized-keys':[key]},'packages':['openssh-server','python3-pexpect','gnome-terminal'],'updates':'security','shutdown':'poweroff','late-commands':["printf '[daemon]\\nAutomaticLoginEnable=true\\nAutomaticLogin=hextest\\nWaylandEnable=false\\n' > /target/etc/gdm3/custom.conf","printf 'hextest ALL=(ALL) NOPASSWD:ALL\\n' > /target/etc/sudoers.d/90-hextest-ci","chmod 440 /target/etc/sudoers.d/90-hextest-ci"]}}
pathlib.Path('seed/user-data').write_text('#cloud-config\n'+json.dumps(config))
pathlib.Path('seed/meta-data').write_text('instance-id: hex-desktop-ci\nlocal-hostname: hex-desktop-ci\n')
PY
python3 -m http.server 8765 --bind 127.0.0.1 --directory seed > "$base/seed-http.log" 2>&1 & seed_pid=$!
qemu-img create -f qcow2 desktop.qcow2 42G
qmp_capture() {
  python3 - "$work/qmp.sock" "$base/$1" <<'PY'
import json,socket,sys
s=socket.socket(socket.AF_UNIX);s.connect(sys.argv[1]);f=s.makefile('rwb',buffering=0);f.readline()
for cmd in [{'execute':'qmp_capabilities'},{'execute':'screendump','arguments':{'filename':sys.argv[2]}}]:
 f.write((json.dumps(cmd)+'\n').encode())
 while True:
  r=json.loads(f.readline())
  if 'return' in r:break
  if 'error' in r:raise RuntimeError(r)
PY
}
qemu-system-x86_64 -enable-kvm -cpu host -smp 4 -m 8192 -drive file=desktop.qcow2,format=qcow2,if=virtio -cdrom "$iso" -kernel vmlinuz -initrd initrd -append 'boot=casper autoinstall ds=nocloud-net;s=http://10.0.2.2:8765/ console=ttyS0' -nic user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:2222-:22 -vga std -display none -qmp unix:"$work/qmp.sock",server=on,wait=off -serial file:"$base/installer-serial.log" -daemonize -pidfile "$work/vm.pid" -no-reboot
# Desktop autoinstall must actually power off; a timeout is not a pass.
for n in $(seq 1 180); do
  kill -0 "$(cat "$work/vm.pid")" 2>/dev/null || break
  if (( n==180 )); then qmp_capture installer-timeout.ppm || true; echo 'Desktop installation timed out'; exit 1; fi
  sleep 10
done
rm -f "$work/qmp.sock" "$work/vm.pid"
qemu-system-x86_64 -enable-kvm -cpu host -smp 4 -m 8192 -drive file=desktop.qcow2,format=qcow2,if=virtio -nic user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:2222-:22 -vga std -display none -qmp unix:"$work/qmp.sock",server=on,wait=off -serial file:"$base/desktop-serial.log" -daemonize -pidfile "$work/vm.pid"
ssh_args=(-i "$work/key" -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5)
guest() { ssh "${ssh_args[@]}" hextest@127.0.0.1 "$@"; }
for n in $(seq 1 90); do guest true 2>/dev/null && break; (( n<90 )) || { qmp_capture boot-timeout.ppm; exit 1; }; sleep 5; done
guest 'set -eu; . /etc/os-release; test "$VERSION_ID" = 24.04; test "$(uname -m)" = x86_64; ! systemd-detect-virt --container; dpkg-query -W ubuntu-desktop-minimal; test ! -e /usr/bin/docker; cat /etc/os-release; free -m; df -h /' > "$base/desktop-environment.txt"
for n in $(seq 1 60); do guest 'pgrep -u hextest gnome-shell' >/dev/null 2>&1 && break; (( n<60 )) || { qmp_capture gnome-timeout.ppm; exit 1; }; sleep 5; done
scp -i "$work/key" -P 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$GITHUB_WORKSPACE/scripts/ubuntu-server.sh" "$GITHUB_WORKSPACE/scripts/desktop-guest.py" hextest@127.0.0.1:/home/hextest/
guest 'DISPLAY=:0 xrandr --output $(DISPLAY=:0 xrandr | awk '\''/ connected/ {print $1; exit}'\'') --mode 1280x1024'
guest 'DISPLAY=:0 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus gnome-terminal --geometry=145x45 -- bash -c "python3 /home/hextest/desktop-guest.py > >(tee /home/hextest/desktop-test.log) 2>&1; exec bash"'
for n in $(seq 1 180); do
  if guest 'test -f acceptance/menu-ready' 2>/dev/null; then break; fi
  if guest 'test -f acceptance/failed' 2>/dev/null; then guest 'cat desktop-test.log' > "$base/desktop-test.log"; qmp_capture failed.ppm; exit 1; fi
  (( n<180 )) || { guest 'cat desktop-test.log' > "$base/desktop-test.log"; qmp_capture bootstrap-timeout.ppm; exit 1; }
  sleep 10
done
qmp_capture desktop-menu.ppm
guest 'touch acceptance/screenshot-taken'
sleep 5
guest 'test -f acceptance/success; cd /opt/hexthings; sudo docker compose ps; curl -fsS http://127.0.0.1:8080/healthz; sudo docker compose exec -T api /bin/tenantctl list | grep -E "^desktop-ci "' > "$base/result.txt"
guest 'cat desktop-test.log' > "$base/desktop-test.log"
guest 'sudo bash -c '\''cd /opt/hexthings; ids=$(docker compose ps -q); docker inspect $ids'\''' | python3 -c 'import json,sys; print(json.dumps([{"Name":x["Name"],"State":{"Running":x["State"]["Running"]},"NetworkSettings":{"Ports":x["NetworkSettings"]["Ports"]}} for x in json.load(sys.stdin)]))' > "$base/container-bindings.json"
python3 - "$base" <<'PY'
import json,pathlib,sys
from PIL import Image
p=pathlib.Path(sys.argv[1]);n=0
for c in json.loads((p/'container-bindings.json').read_text()):
 assert c['State']['Running'],c['Name']
 for bindings in (c['NetworkSettings'].get('Ports') or {}).values():
  for b in bindings or []:
   n+=1;assert b['HostIp']=='127.0.0.1',(c['Name'],b)
assert n==6,n
for img in p.glob('*.ppm'):
 Image.open(img).save(img.with_suffix('.png'));img.unlink()
print('Real Desktop24.04 installation, fresh apt Docker provisioning, guided setup, automatic menu handoff and six loopback bindings passed.')
PY
