#!/usr/bin/env bash
# HexThings single-server Ubuntu installer. Downloads source; not an offline bundle.
# Run: sudo bash ubuntu-server.sh [--yes] [--check] [--dir /opt/hexthings]
set -euo pipefail
ubuntu_supported() {
  [[ $1 == ubuntu && $2 =~ ^(22\.04|24\.04|26\.04|26\.10)$ && $3 == amd64 ]]
}
resources_ready() { (( $1 >= 4000 && $2 >= 10240 )); }
main() {
  local yes=0 check=0 dir=/opt/hexthings
  while (( $# )); do
    case $1 in
      --yes) yes=1;; --check) check=1;;
      --dir) [[ $# -ge 2 && $2 == /* ]] || { echo '--dir needs an absolute path' >&2; return 2; }; dir=$2; shift;;
      --help|-h) sed -n '2,3p' "${BASH_SOURCE[0]}"; return 0;;
      *) echo "Unknown option: $1" >&2; return 2;;
    esac; shift
  done
  [[ -f /etc/os-release ]] || { echo 'Cannot identify Ubuntu.' >&2; return 1; }
  local ID='' VERSION_ID='' VERSION_CODENAME='' UBUNTU_CODENAME=''
  . /etc/os-release
  local arch; arch=$(dpkg --print-architecture)
  ubuntu_supported "$ID" "$VERSION_ID" "$arch" || {
    echo 'Requires Ubuntu 22.04, 24.04, 26.04 or 26.10 on amd64. Other architectures are not verified by this installer.' >&2; return 1;
  }
  [[ $(id -u) == 0 || $check == 1 ]] || { echo 'Run with sudo bash ubuntu-server.sh (root rights are needed for apt and Docker).' >&2; return 1; }
  [[ $dir != / && ! -e $dir && ! -L $dir ]] || { echo "Destination already exists or is unsafe: $dir. No update or overwrite attempted." >&2; return 1; }
  local parent; parent=$(dirname "$dir")
  [[ -d $parent && ! -L $parent ]] || { echo 'Destination parent must already exist and must not be a symlink.' >&2; return 1; }
  local mem disk
  mem=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
  disk=$(df -Pm "$parent" | awk 'NR==2 {print $4}')
  resources_ready "$mem" "$disk" || { echo "Need at least 4000MiB RAM and 10GiB free disk (found ${mem}MiB / ${disk}MiB). Local AI needs more and stays off." >&2; return 1; }
  # Refuse conflicting distribution packages rather than remove other software.
  local pkg conflicts=''
  if ! command -v docker >/dev/null 2>&1; then
    for pkg in docker.io docker-compose docker-compose-v2 docker-doc docker-buildx podman-docker containerd runc; do
      if [[ $(dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null || true) == 'install ok installed' ]]; then conflicts+=" $pkg"; fi
    done
    [[ -z $conflicts ]] || { echo "Conflicting packages:$conflicts. Review Docker's Ubuntu migration instructions; nothing will be removed automatically." >&2; return 1; }
  fi
  local missing=() tool package
  for tool in curl git tar gzip ss python3; do
    case $tool in ss) package=iproute2;; *) package=$tool;; esac
    command -v "$tool" >/dev/null 2>&1 || missing+=("$package")
  done
  [[ $(dpkg-query -W -f='${Status}' ca-certificates 2>/dev/null || true) == 'install ok installed' ]] || missing+=(ca-certificates)
  local docker_install=0 docker_start=0
  if ! command -v docker >/dev/null 2>&1; then docker_install=1
  else
    docker compose version >/dev/null 2>&1 || { echo 'Existing Docker lacks Compose v2. Repair its existing installation first; this script will not replace it.' >&2; return 1; }
    docker info >/dev/null 2>&1 || docker_start=1
  fi
  if command -v ss >/dev/null 2>&1; then
    local port
    for port in 8080 8000 1883 5432 9200 8100; do
      if ss -H -ltn | awk '{print $4}' | grep -Eq "[:.]${port}$"; then echo "Port $port is in use. No installation attempted." >&2; return 1; fi
    done
  fi
  if (( docker_install || docker_start )); then command -v systemctl >/dev/null || { echo 'systemd is required for this server installer.' >&2; return 1; }; fi
  echo "Ubuntu $VERSION_ID/$arch, ${mem}MiB RAM, ${disk}MiB free. Destination: $dir"
  echo "Missing base packages: ${missing[*]:-none}"
  echo "Install Docker from official apt repository: $docker_install; start existing Docker: $docker_start"
  echo 'This downloads/builds HexThings, generates local secrets and creates the first workspace/admin.'
  echo 'All published services bind to127.0.0.1. AI stays off. No firewall rules or paid services are enabled.'
  echo 'Docker has root-equivalent host access. Source/image builds need internet and take several minutes.'
  [[ $check == 0 ]] || { echo 'Check only: no packages, files or services changed.'; return 0; }
  if [[ $yes == 0 ]]; then
    local answer=''
    read -r -p 'Proceed with the plan above? [y/N] ' answer </dev/tty || return 2
    [[ $answer == y || $answer == Y || $answer == yes ]] || return 2
  fi
  if (( ${#missing[@]} )); then apt-get update; apt-get install -y --no-install-recommends "${missing[@]}"; fi
  if (( docker_install )); then

    install -m0755 -d /etc/apt/keyrings
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
    # Refuse to overwrite an administrator's existing repository file.
    [[ ! -e /etc/apt/sources.list.d/hexthings-docker.sources ]] || { echo 'hexthings-docker.sources already exists; review it before retrying.' >&2; return 1; }
    printf 'Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n' "${UBUNTU_CODENAME:-$VERSION_CODENAME}" "$arch" > /etc/apt/sources.list.d/hexthings-docker.sources
    apt-get update
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    systemctl enable --now docker
  elif (( docker_start )); then systemctl start docker; fi
  docker info >/dev/null; docker compose version >/dev/null
  # Repeat now ss is installed, before downloading source or starting containers.
  local port
  for port in 8080 8000 1883 5432 9200 8100; do
    if ss -H -ltn | awk '{print $4}' | grep -Eq "[:.]${port}$"; then echo "Port $port is in use. Dependencies may be installed, but HexThings was not started." >&2; return 1; fi
  done
  local staging; staging=$(mktemp -d "$parent/.hexthings-source.XXXXXXXX")
  if ! GIT_TERMINAL_PROMPT=0 git clone --depth 1 https://github.com/abhisekrath00-ux/iot-platform.git "$staging/source"; then
    rm -rf "$staging"; echo 'Source download failed. Repository must be public; no token is requested.' >&2; return 1
  fi
  mv "$staging/source" "$dir"; rmdir "$staging"
  cd "$dir"
  # Reject a downloaded older/unsafe source rather than run broad bindings.
  docker compose --env-file .env.example config --format json | loopback_binding_check
  local install_args=(--no-ai)
  [[ $yes == 0 ]] || install_args+=(--yes)
  bash scripts/install.sh "${install_args[@]}" || return $?
  echo 'On your laptop: ssh -L 8080:127.0.0.1:8080 YOUR_SSH_USER@YOUR_SERVER'
  echo 'Then open http://localhost:8080 and complete the required first-sign-in change.'
  echo "Source and install.log: $dir. No public HTTP/TLS endpoint was configured."
  finish_setup "$dir" "$yes"
}
finish_setup() {
  local source_dir=$1 unattended=$2
  echo "Open the HexThings terminal menu later: sudo bash $source_dir/scripts/hexthings"
  if [[ $unattended == 0 && -t 0 && -t 1 ]]; then
    echo 'Setup succeeded. Opening the HexThings terminal menu (q to quit).'
    # Menu failure must not turn a completed installation into a failed install.
    if ! bash "$source_dir/scripts/hexthings"; then
      echo 'HexThings is installed, but the terminal menu exited with an error. Use the command above to reopen it.' >&2
    fi
  fi
}
loopback_binding_check() {
  python3 -c 'import json,sys
x=json.load(sys.stdin)
services=x.get("services",{})
assert services and services.get("web",{}).get("ports"), "missing web ports"
for name,s in services.items():
    assert s.get("network_mode") != "host", "host network: "+name
    for p in s.get("ports",[]):
        assert isinstance(p,dict) and p.get("host_ip")=="127.0.0.1", "non-loopback port: "+name
print("All published Compose services are loopback-only")'
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
