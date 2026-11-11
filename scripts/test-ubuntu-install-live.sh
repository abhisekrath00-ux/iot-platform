#!/usr/bin/env bash
# Destructive disposable-CI-only guided installer acceptance, not a host installer.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted ]] || { echo 'Refusing outside disposable GitHub-hosted CI' >&2; exit 2; }
cd "$(dirname "$0")/.."
source scripts/ubuntu-server.sh
cleanup() { rm -f /tmp/hexthings-ci-admin-password; docker compose down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup
rm -f .env
umask 077
python3 -c 'import secrets;print(secrets.token_urlsafe(24))' > /tmp/hexthings-ci-admin-password
bash scripts/install.sh --yes --admin-password-stdin --no-ai --workspace ubuntu-ci --admin-email ubuntu-ci@example.invalid < /tmp/hexthings-ci-admin-password
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
# Real local login accepts only the chosen password, before and after rerun.
python3 - <<'PYLOGIN'
import json,urllib.request,urllib.error
pw=open('/tmp/hexthings-ci-admin-password').read().rstrip('\n')
url='http://127.0.0.1:8080/auth/login'
for password,expected in [(pw,200),('Hex@2026',401)]:
 req=urllib.request.Request(url,data=json.dumps({'email':'ubuntu-ci@example.invalid','password':password}).encode(),headers={'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(req) as response:status=response.status;body=json.load(response)
 except urllib.error.HTTPError as error:status=error.code;body={}
 assert status==expected,status
 if expected==200:assert body.get('token') and not body.get('must_change_credentials')
print('Chosen admin password login passes after preserved rerun; former default rejected')
PYLOGIN
printf 'Actual guided Ubuntu-runner installer, loopback bindings, health, workspace and secret-preserving rerun passed\n'
