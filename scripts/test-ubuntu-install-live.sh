#!/usr/bin/env bash
# Destructive disposable-CI-only guided installer acceptance, not a host installer.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted ]] || { echo 'Refusing outside disposable GitHub-hosted CI' >&2; exit 2; }
cd "$(dirname "$0")/.."
source scripts/ubuntu-server.sh
cleanup() { docker compose down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup
rm -f .env
bash scripts/install.sh --yes --no-ai --workspace ubuntu-ci --admin-email ubuntu-ci@example.invalid
# Validate actual merged Compose configuration and live host bindings.
docker compose config --format json | loopback_binding_check
ids=$(docker compose ps -q)
[[ -n $ids ]]
docker inspect $ids | python3 -c 'import json,sys
containers=json.load(sys.stdin); bindings=0
for c in containers:
    assert c["State"]["Running"], c["Name"]
    for values in (c.get("NetworkSettings",{}).get("Ports",{}) or {}).values():
        for b in values or []:
            bindings+=1
            assert b.get("HostIp")=="127.0.0.1", (c["Name"],b)
assert bindings==6, bindings
print("Six actual host port bindings are127.0.0.1")'
curl -fsS http://127.0.0.1:8080/healthz >/dev/null
docker compose exec -T api /bin/tenantctl list | grep -E '^ubuntu-ci '
[[ $(stat -c '%a' .env) == 600 ]]
# Verify re-run keeps secrets and workspace without printing the secret file.
before=$(sha256sum .env)
bash scripts/install.sh --yes --no-ai --workspace ubuntu-ci --admin-email ubuntu-ci@example.invalid
[[ $(sha256sum .env) == "$before" ]]
printf 'Actual guided Ubuntu-runner installer, loopback bindings, health, workspace and secret-preserving rerun passed\n'
