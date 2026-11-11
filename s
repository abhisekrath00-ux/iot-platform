#!/usr/bin/env bash
# Local operator recovery. No platform rebuild/restart, source replacement, or email needed.
set +x
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo 'Run with sudo bash recover-admin-local.sh';exit 1; }
[ $# = 2 ] || { echo 'Usage: sudo bash recover-admin-local.sh WORKSPACE ADMIN_EMAIL';exit 2; }
workspace=$1;email=$2
[ -r /dev/tty ] && [ -w /dev/tty ] || { echo 'An interactive local terminal is required';exit 1; }
cd /opt/hexthings
[ -f docker-compose.yml ] || { echo 'Expected installation folder /opt/hexthings not found';exit 1; }
docker info >/dev/null
docker compose exec -T api sh -c '[ "$LOCAL_LOGIN" = 1 ]' || { echo 'Running local-login API required';exit 1; }
tmp=$(mktemp -d /tmp/hexthings-recovery.XXXXXXXX)
remote="/tmp/$(basename "$tmp")-tool"
cleanup() { unset password repeat;docker compose exec -T api rm -f "$remote" >/dev/null 2>&1 || true;rm -rf "$tmp"; }
trap cleanup EXIT
trap 'exit 130' INT TERM HUP
revision=951860e7f988c749a335d99a8e9cb2d588336273
echo 'Preparing the recovery tool for this machine. This downloads a temporary Go builder and source/dependencies; the platform is not rebuilt or restarted.'
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "https://codeload.github.com/abhisekrath00-ux/iot-platform/tar.gz/$revision" -o "$tmp/source.tar.gz"
mkdir "$tmp/source" "$tmp/out"
tar -xzf "$tmp/source.tar.gz" --strip-components=1 -C "$tmp/source"
docker run --rm --mount "type=bind,src=$tmp/source/server,dst=/src,readonly" --mount "type=bind,src=$tmp/out,dst=/out" -w /src -e CGO_ENABLED=0 golang:1.26-alpine go build -trimpath -ldflags '-s -w' -o /out/recover ./cmd/tenantctl
chmod 755 "$tmp/out/recover"
docker compose cp "$tmp/out/recover" "api:$remote"
printf 'Reset only the active admin %s in workspace %s. Workspace/data and MFA stay unchanged. Existing login sessions will end.\n' "$email" "$workspace"
IFS= read -r -p 'Type RESET to continue, or anything else to cancel: ' confirm </dev/tty
[ "$confirm" = RESET ] || { echo 'Cancelled; no account changed';exit 0; }
IFS= read -r -s -p 'New password (12-128 characters, hidden): ' password </dev/tty;printf '\n' >/dev/tty
IFS= read -r -s -p 'Confirm new password (hidden): ' repeat </dev/tty;printf '\n' >/dev/tty
[ "$password" = "$repeat" ] || { echo 'Passwords differ; no account changed';exit 1; }
# Bash builtin printf sends the secret through stdin, never command-line arguments.
printf '%s\n' "$password" | docker compose exec -T api "$remote" recover-admin --workspace "$workspace" --email "$email" --confirm
unset password repeat
echo 'Sign in in the Ubuntu browser with that email and your new password. Any existing first-sign-in change requirement still applies.'
