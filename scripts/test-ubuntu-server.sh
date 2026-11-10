#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
source "$ROOT/scripts/ubuntu-server.sh"
ubuntu_supported ubuntu 22.04 amd64
ubuntu_supported ubuntu 24.04 amd64
ubuntu_supported ubuntu 26.04 amd64
ubuntu_supported ubuntu 26.10 amd64
! ubuntu_supported debian 12 amd64
! ubuntu_supported ubuntu 20.04 amd64
! ubuntu_supported ubuntu 24.04 arm64
resources_ready 4000 10240
! resources_ready 3999 10240
! resources_ready 4000 10239
printf '%s' '{"services":{"web":{"ports":[{"host_ip":"127.0.0.1","published":"8080","target":80}]},"api":{"ports":[{"host_ip":"127.0.0.1"}]}}}' | loopback_binding_check
for data in '{"services":{"web":{"ports":[{"host_ip":"0.0.0.0"}]}}}' '{"services":{"web":{"ports":[{"host_ip":"127.0.0.1"}]},"api":{"ports":[{"host_ip":"0.0.0.0"}]}}}' '{"services":{"web":{"ports":[{"host_ip":"127.0.0.1"}]},"api":{"network_mode":"host"}}}' '{"services":{"web":{"ports":["8080:80"]}}}' '{}' 'invalid'; do
  if printf '%s' "$data" | loopback_binding_check >/dev/null 2>&1; then echo 'unsafe Compose accepted' >&2; exit 1; fi
done
bash "$ROOT/scripts/ubuntu-server.sh" --help >/dev/null
if bash "$ROOT/scripts/ubuntu-server.sh" --dir relative >/dev/null 2>&1; then exit 1; fi
python3 - "$ROOT" <<'PY'
import sys,re
from pathlib import Path
s=(Path(sys.argv[1])/'docker-compose.yml').read_text()
ports=re.findall(r'"([^"\n]*:[^"\n]*)"',s)
# Read only the six explicit published-port lines, not environment values.
expected=['127.0.0.1:5432:5432','127.0.0.1:1883:1883','127.0.0.1:8000:8000','127.0.0.1:9200:9200','127.0.0.1:8100:8100','127.0.0.1:${WEB_PORT:-8080}:80']
assert all(s.count('"'+p+'"')==1 for p in expected)
assert sum('ports:' in line for line in s.splitlines())==6
print('Six source bindings are loopback-only; WEB_PORT is honored')
PY
printf 'Ubuntu OS/resource/unsafe binding/source contract checks passed\n'
# Exercise main without installing packages, starting Docker or using the network.
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export TEST_INSTALL_LOG="$T/actions"
resources_ready() { return 0; }
id() { echo 0; }
dpkg() { echo amd64; }
dpkg-query() { echo 'install ok installed'; }
ss() { [[ ${TEST_BUSY:-0} == 0 ]] || echo 'LISTEN 0 1 127.0.0.1:8080 0.0.0.0:*'; return 0; }
docker() {
  case "$*" in
    *'config --format json') printf '%s' '{"services":{"web":{"ports":[{"host_ip":"127.0.0.1"}]}}}';;
    *) return 0;;
  esac
}
git() {
  echo clone >> "$TEST_INSTALL_LOG"
  [[ ${TEST_CLONE_FAIL:-0} == 0 ]] || return 1
  local dest=${!#}; mkdir -p "$dest/scripts"; : > "$dest/.env.example"
  printf '#!/bin/bash\nprintf "install %%s\\n" "$*" >> "$TEST_INSTALL_LOG"\n' > "$dest/scripts/install.sh"
}
main --yes --dir "$T/installed" > "$T/success.out"
grep -Fx 'clone' "$TEST_INSTALL_LOG"
grep -Fx 'install --no-ai --yes' "$TEST_INSTALL_LOG"
[[ -f "$T/installed/scripts/install.sh" ]]
before=$(wc -l < "$TEST_INSTALL_LOG")
if main --yes --dir "$T/installed" > /dev/null 2>&1; then exit 1; fi
if TEST_BUSY=1 main --yes --dir "$T/busy" > /dev/null 2>&1; then exit 1; fi
[[ $(wc -l < "$TEST_INSTALL_LOG") == "$before" && ! -e "$T/busy" ]]
main --check --dir "$T/check" > "$T/check.out"
[[ $(wc -l < "$TEST_INSTALL_LOG") == "$before" && ! -e "$T/check" ]]
# A failed clone must not create the final destination or run the installer.
if TEST_CLONE_FAIL=1 main --yes --dir "$T/download-failed" > /dev/null 2>&1; then exit 1; fi
[[ ! -e "$T/download-failed" && $(grep -c '^install ' "$TEST_INSTALL_LOG") == 1 ]]
printf 'Stubbed fresh install/check-only/existing destination/busy port/clone refusal checks passed\n'

# Noninteractive completion must not launch the menu, including guided mode.
mkdir -p "$T/menu/scripts"
printf '#!/bin/bash\necho menu >> "$TEST_INSTALL_LOG"\n' > "$T/menu/scripts/hexthings"
finish_setup "$T/menu" 0 > "$T/menu-out"
finish_setup "$T/menu" 1 >> "$T/menu-out"
! grep -q '^menu$' "$TEST_INSTALL_LOG"
grep -q 'Open the HexThings terminal menu later' "$T/menu-out"
printf 'Noninteractive menu handoff stays nonblocking\n'
