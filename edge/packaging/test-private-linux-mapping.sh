#!/usr/bin/env bash
# Proposal-only disposable hosted VM acceptance. Never run on an owner host.
set -euo pipefail
umask 077
[[ ${HEX_DISPOSABLE_LINUX_MAPPING_REVIEW:-} == 1 ]] || { echo 'Explicit disposable review marker required.' >&2; exit 2; }
[[ $(id -u) == 0 && ${GITHUB_ACTIONS:-} == true && ${RUNNER_OS:-} == Linux ]] || { echo 'Disposable Linux Actions root context required.' >&2; exit 2; }
[[ $# == 1 && -f $1 && ! -L $1 && -x $1 ]] || { echo 'Exact compiled product binary required.' >&2; exit 2; }
# No activation is tested here. Only ownership, service-user lint/access,
# generated-unit parsing and installed-but-stopped/static state are in scope.
source_binary=$(realpath "$1")
id="hexreview$(openssl rand -hex 4)"
user=$id
opt="/opt/$id"; etc="/etc/$id"; state="/var/lib/$id"; unit="$id.service"
cleanup() {
 systemctl stop "$unit" 2>/dev/null || true
 rm -f "/etc/systemd/system/$unit"
 systemctl daemon-reload || true
 userdel "$user" 2>/dev/null || true
 rm -rf -- "$opt" "$etc" "$state"
}
for p in "$opt" "$etc" "$state" "/etc/systemd/system/$unit"; do [[ ! -e $p && ! -L $p ]] || { echo 'Unique fixture path collision.' >&2; exit 2; }; done
! id "$user" >/dev/null 2>&1 || { echo 'Unique fixture user collision.' >&2; exit 2; }
trap cleanup EXIT
useradd --system --user-group --no-create-home --shell /usr/sbin/nologin "$user"
install -d -m 0755 "$opt"
install -d -m 0700 "$etc" "$state" "$etc/identity" "$etc/inputs"
install -d -m 0750 -o "$user" -g "$user" "$state/runtime"
install -m 0755 -o root -g root "$source_binary" "$opt/edge-agent"
openssl ecparam -name prime256v1 -genkey -noout -out "$etc/ca.key" 2>/dev/null
openssl req -new -x509 -key "$etc/ca.key" -subj /CN=SyntheticReviewCA -days 1 -out "$etc/identity/device-ca.pem" 2>/dev/null
openssl ecparam -name prime256v1 -genkey -noout -out "$etc/identity/identity.key" 2>/dev/null
openssl req -new -key "$etc/identity/identity.key" -subj /CN=synthetic-review-serial -out "$etc/client.csr" 2>/dev/null
printf '%s\n' 'extendedKeyUsage=clientAuth' 'keyUsage=digitalSignature' > "$etc/client.ext"
openssl x509 -req -in "$etc/client.csr" -CA "$etc/identity/device-ca.pem" -CAkey "$etc/ca.key" -set_serial 2 -days 1 -extfile "$etc/client.ext" -out "$etc/identity/identity.crt" 2>/dev/null
printf '%s\n' '{"gateway_id":"synthetic-review-gateway","ingest_token":"synthetic-review-not-a-server-token"}' > "$etc/identity/identity.json"
cp "$etc/identity/device-ca.pem" "$etc/inputs/broker-ca.pem"
cat > "$etc/inputs/server-config.yaml" <<'CONFIG'
gateway_id: synthetic-review-gateway
tenant_id: synthetic-review-tenant
serial: synthetic-review-serial
mqtt: {host: broker.example.invalid, port: 8883, tls: true}
autodetect: {enabled: false}
devices: []
CONFIG
chmod 0600 "$etc/identity/"* "$etc/inputs/"*
python3 - "$etc" "$opt" "$state" "$user" <<'PY' > "$etc/request.json"
import json,sys
etc,opt,state,user=sys.argv[1:]
print(json.dumps(dict(IdentityDir=etc+'/identity',InputDir=etc+'/inputs',Serial='synthetic-review-serial',Tenant='synthetic-review-tenant',Binary=opt+'/edge-agent',ConfigPath=etc+'/runtime.yaml',StateDir=state+'/runtime',User=user)))
PY
"$opt/edge-agent" -review-runtime-mapping < "$etc/request.json" > "$etc/mapping.json"
python3 - "$etc" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]);m=json.loads((p/'mapping.json').read_text())
assert m['CanStart'] is False and '[Install]' not in m['Unit']
(p/'runtime.yaml').write_text(m['ConfigYAML']);(p/'review.service').write_text(m['Unit'])
PY
chown -R "root:$user" "$etc"
chown "root:$user" "$state"
chmod 0750 "$etc" "$etc/identity" "$etc/inputs" "$state"
chmod 0640 "$etc/runtime.yaml" "$etc/identity/"* "$etc/inputs/broker-ca.pem"
# CA signing key is never readable by the service user.
chmod 0600 "$etc/ca.key" "$etc/client.csr" "$etc/client.ext" "$etc/request.json" "$etc/mapping.json" "$etc/review.service" "$etc/inputs/server-config.yaml"
chown root:root "$etc/ca.key" "$etc/client.csr" "$etc/client.ext" "$etc/request.json" "$etc/mapping.json" "$etc/review.service" "$etc/inputs/server-config.yaml"
runuser -u "$user" -- "$opt/edge-agent" -check-config -config "$etc/runtime.yaml" -identity-dir "$etc/identity"
runuser -u "$user" -- test -r "$etc/identity/identity.key"
! runuser -u "$user" -- test -w "$etc/identity/identity.key"
! runuser -u "$user" -- test -w "$etc/runtime.yaml"
runuser -u "$user" -- touch "$state/runtime/access-check"
for secret in ca.key client.csr client.ext request.json mapping.json review.service inputs/server-config.yaml; do
 ! runuser -u "$user" -- test -r "$etc/$secret"
done
install -m 0644 "$etc/review.service" "/etc/systemd/system/$unit"
systemd-analyze verify "/etc/systemd/system/$unit"
systemctl daemon-reload
[[ $(systemctl is-enabled "$unit" 2>/dev/null || true) == static ]]
[[ $(systemctl is-active "$unit" 2>/dev/null || true) == inactive ]]
[[ $(stat -c '%U:%G:%a' "$opt/edge-agent") == root:root:755 ]]
echo 'PASS synthetic mapping ownership, service-user config lint/read-only identity and writable runtime state, unit parse/static/inactive. No service start or broker proof.'
