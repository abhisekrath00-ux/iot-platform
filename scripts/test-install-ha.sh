#!/usr/bin/env bash
# Tests the HA generator only (validation, rendering, secrets handling, file syntax). It cannot start
# etcd, Patroni, HAProxy or keepalived: that needs 3 real machines (see docs/ha-multi-node.md).
set -uo pipefail
cd "$(dirname "$0")/.."
S="bash scripts/install-ha.sh"; T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; fails=0
ok() { echo "ok   $1"; }; bad() { echo "FAIL $1"; fails=$((fails+1)); }
expect_fail() { local m="$1"; shift; if "$@" >/dev/null 2>&1; then bad "$m (should have failed)"; else ok "$m"; fi; }
N=a=10.0.0.11,b=10.0.0.12,c=10.0.0.13
bash -n scripts/install-ha.sh && ok "bash syntax" || bad "bash syntax"
expect_fail "two nodes refused"      $S render --nodes a=10.0.0.11,b=10.0.0.12 --vip 10.0.0.10 --out $T/x1
expect_fail "four nodes refused"     $S render --nodes a=10.0.0.11,b=10.0.0.12,c=10.0.0.13,d=10.0.0.14 --vip 10.0.0.10 --out $T/x2
expect_fail "duplicate name refused" $S render --nodes a=10.0.0.11,a=10.0.0.12,c=10.0.0.13 --vip 10.0.0.10 --out $T/x3
expect_fail "duplicate ip refused"   $S render --nodes a=10.0.0.11,b=10.0.0.11,c=10.0.0.13 --vip 10.0.0.10 --out $T/x4
expect_fail "bad ip refused"         $S render --nodes a=10.0.0.999,b=10.0.0.12,c=10.0.0.13 --vip 10.0.0.10 --out $T/x5
expect_fail "vip equals node refused" $S render --nodes $N --vip 10.0.0.12 --out $T/x6
expect_fail "missing vip refused"    $S render --nodes $N --out $T/x7
expect_fail "bad node name refused"  $S render --nodes 'A_1=10.0.0.11,b=10.0.0.12,c=10.0.0.13' --vip 10.0.0.10 --out $T/x8
expect_fail "unknown option refused" $S render --nodes $N --vip 10.0.0.10 --bogus
[ ! -e $T/x1 ] && ok "no output on refused input" || bad "output created on refused input"
$S render --nodes $N --vip 10.0.0.10 --iface ens18 --out $T/o >/dev/null && ok "render works" || bad "render"
for n in a b c; do for f in docker-compose.yml patroni.yml haproxy.cfg keepalived.conf .env README.txt; do [ -s $T/o/$n/$f ] || bad "missing $n/$f"; done; done
python3 - "$T/o" <<'PY' && ok "yaml parses, structure correct" || bad "yaml structure"
import sys,yaml
root=sys.argv[1]
for n,ip in (("a","10.0.0.11"),("b","10.0.0.12"),("c","10.0.0.13")):
    c=yaml.safe_load(open(f"{root}/{n}/docker-compose.yml"))
    for svc in ("etcd","patroni","haproxy","keepalived","mosquitto","api","ingest","mcp","web"):
        assert svc in c["services"],(n,svc)
    assert c["services"]["haproxy"]["image"] == "haproxy:3.2.26-alpine"
    env=c["services"]["api"]["environment"]
    assert env["OAUTH_STATE_BACKEND"] == "postgres"
    assert "REDIS_URL" not in env and env["CACHE_BACKEND"] == "off"
    assert "redis" not in c["services"] and "redisdata" not in c["volumes"]
    assert c["services"]["api"]["depends_on"] == ["mosquitto"]
    assert "REDIS_PASSWORD" not in open(f"{root}/{n}/.env").read()
    assert ip in " ".join(c["services"]["etcd"]["command"].split()),n
    p=yaml.safe_load(open(f"{root}/{n}/patroni.yml"))
    assert p["name"]==n and p["scope"]=="hexthings"
    assert p["bootstrap"]["dcs"]["synchronous_mode"] is True
    assert p["bootstrap"]["dcs"]["synchronous_mode_strict"] is False
    assert "password" not in str(p["postgresql"]["authentication"]).lower()
    assert p["postgresql"]["connect_address"]==ip+":5432"
    assert len(p["etcd3"]["hosts"].split(","))==3
PY
for n in a b c; do
  [ "$(stat -c %a $T/o/$n/.env)" = 600 ] && ok "$n .env is mode 600" || bad "$n .env mode"
  [ "$(stat -c %a $T/o/$n/keepalived.conf)" = 600 ] && ok "$n keepalived.conf is mode 600" || bad "$n keepalived mode"
done
cmp -s $T/o/a/.env $T/o/b/.env && cmp -s $T/o/b/.env $T/o/c/.env && ok "all nodes share one secret set" || bad "secrets differ between nodes"
sec=$(grep POSTGRES_SUPERUSER_PASSWORD $T/o/a/.env | cut -d= -f2)
[ ${#sec} -ge 32 ] && ok "generated secret length >= 32" || bad "secret too short"
grep -rq "$sec" $T/o/*/docker-compose.yml $T/o/*/patroni.yml $T/o/*/haproxy.cfg && bad "secret leaked into non-secret file" || ok "secret not written into compose, patroni or haproxy files"
$S render --nodes $N --vip 10.0.0.10 --out $T/o2 >/dev/null; ! cmp -s $T/o/a/.env $T/o2/a/.env && ok "each render generates fresh secrets" || bad "secrets reused"
grep -q "priority 100" $T/o/a/keepalived.conf && grep -q "priority 90" $T/o/b/keepalived.conf && grep -q "priority 80" $T/o/c/keepalived.conf && ok "keepalived priorities distinct" || bad "priorities"
grep -q "interface ens18" $T/o/a/keepalived.conf && ok "interface honoured" || bad "interface"
grep -q "option httpchk GET /primary" $T/o/a/haproxy.cfg && [ "$(grep -c 'check port 8008' $T/o/a/haproxy.cfg)" = 3 ] && ok "haproxy routes Postgres to the Patroni primary" || bad "haproxy primary check"
python3 - "$T/o/a/haproxy.cfg" <<'PYTEST' && ok "web/api require HTTP 200 health, PG/MQTT stay TCP" || bad "HTTP health gates"
import re,sys
s=open(sys.argv[1]).read()
for name in ("web", "api"):
    block=re.search(r"(?ms)^listen "+name+r"\n(.*?)(?=^listen |\Z)",s)[1]
    assert "option httpchk GET /readyz" in block,name
    assert "http-check expect status 200" in block,name
    assert "balance roundrobin" in block,name
assert "mode http" not in s # preserve existing transport; HTTP checks do not replay writes
PYTEST
[ "$(grep -c ' backup$' $T/o/a/haproxy.cfg)" = 2 ] && ok "mqtt: one active broker, two backups" || bad "mqtt backup flags"
$S render --nodes $N --vip 10.0.0.10 --out $T/o3 --sync-strict >/dev/null; grep -q "synchronous_mode_strict: true" $T/o3/a/patroni.yml && ok "--sync-strict honoured" || bad "sync strict"
expect_fail "non-empty output dir refused" $S render --nodes $N --vip 10.0.0.10 --out $T/o
P=$($S plan --nodes $N --vip 10.0.0.10); echo "$P" | grep -q "Design target (UNVERIFIED)" && ok "plan labels unverified target" || bad "plan"
D=$($S drill --vip 10.0.0.10); echo "$D" | grep -q "marker" && ok "drill prints steps" || bad "drill"
# entry point: single hands over to install.sh untouched, multi to install-ha.sh
bash -n scripts/setup.sh && ok "setup.sh syntax" || bad "setup.sh syntax"
H=$(bash scripts/setup.sh single --help 2>&1); echo "$H" | grep -qi "install" && ok "setup.sh single reaches install.sh" || bad "setup single"
M=$(bash scripts/setup.sh multi plan --nodes $N --vip 10.0.0.10); echo "$M" | grep -q "any ONE node" && ok "setup.sh multi reaches install-ha.sh" || bad "setup multi"
bash scripts/setup.sh bogus >/dev/null 2>&1 && bad "setup.sh bogus mode accepted" || ok "setup.sh rejects unknown mode"
bash scripts/setup.sh </dev/null >/dev/null 2>&1 && bad "setup.sh without tty/mode should fail" || ok "setup.sh needs a mode when not interactive"
printf '1\n' | bash scripts/setup.sh >/dev/null 2>&1; true
# drift guard: the terminal look in hx-ui.sh must stay identical to install.sh's wordmark/glyph/footer
ext() { awk '/^glyph\(\)/{p=1} /^banner\(\)/{p=0} p' "$1"; }
[ "$(ext scripts/install.sh)" = "$(ext scripts/hx-ui.sh)" ] && ok "hx-ui.sh wordmark identical to install.sh" || bad "hx-ui.sh drifted from install.sh"
$S render --nodes $N --vip 10.0.0.10 --out $T/shared --shared-cache-mtls >/dev/null && ok "shared TLS cache render" || bad "shared TLS cache render"
python3 - "$T/shared" <<'PYCACHE' && ok "shared TLS cache is explicit, identical and secret-free in compose" || bad "shared TLS cache config"
import sys,yaml,os
root=sys.argv[1];reference=None
for n in ('a','b','c'):
    c=yaml.safe_load(open(f'{root}/{n}/docker-compose.yml'))
    api=c['services']['api'];env=api['environment']
    assert env['OAUTH_STATE_BACKEND']=='postgres'
    assert 'redis' not in c['services'] and 'redisdata' not in c['volumes']
    assert 'REDIS_PASSWORD' not in open(f'{root}/{n}/.env').read()
    assert env['CACHE_BACKEND']=='redis'
    assert env['REDIS_URL']=='rediss://${SHARED_CACHE_AUTHORITY:?Set SHARED_CACHE_AUTHORITY for the external shared TLS cache}'
    for k,name in [('CA','ca.crt'),('CERT','client.crt'),('KEY','client.key')]:
        assert env[f'REDIS_TLS_{k}_FILE']=='/run/cache-tls/'+name
    mount=api['volumes'][0]
    assert mount==dict(type='bind',source='./cache-tls',target='/run/cache-tls',read_only=True,bind=dict(create_host_path=False))
    assert all('volumes' not in c['services'][service] or not any('/run/cache-tls' in str(v) for v in c['services'][service]['volumes']) for service in c['services'] if service!='api')
    text=open(f'{root}/{n}/cache.env').read()
    assert os.stat(f'{root}/{n}/cache.env').st_mode & 0o777 == 0o600
    assert 'SHARED_CACHE_AUTHORITY' not in open(f'{root}/{n}/.env').read()
    assert 'SHARED_CACHE_AUTHORITY=\n' in text
    assert os.stat(f'{root}/{n}/cache-tls').st_mode & 0o777 == 0o700
    assert os.listdir(f'{root}/{n}/cache-tls')==['README.txt']
    if reference is None:reference=env
    else:assert reference==env
PYCACHE
for shared in plain shared; do
  extra=(); [ "$shared" = plain ] || extra+=(--shared-cache-mtls)
  $S render --nodes $N --vip 10.0.0.10 --out "$T/etcd-$shared" --etcd-mtls "${extra[@]}" >/dev/null || bad "etcd TLS render $shared"
  python3 - "$T/etcd-$shared" "$shared" <<'PYETCD' && ok "etcd mTLS structure $shared" || bad "etcd mTLS structure $shared"
import sys,yaml,os
root,mode=sys.argv[1:]
for n,ip in [('a','10.0.0.11'),('b','10.0.0.12'),('c','10.0.0.13')]:
    c=yaml.safe_load(open(f'{root}/{n}/docker-compose.yml'))
    p=yaml.safe_load(open(f'{root}/{n}/patroni.yml'))
    assert c['services']['etcd']['image']=='gcr.io/etcd-development/etcd:v3.5.35'
    cmd=c['services']['etcd']['command']
    assert 'http://' not in cmd
    assert f'--listen-client-urls https://{ip}:2379' in cmd
    assert f'--listen-peer-urls https://{ip}:2380' in cmd
    for peer,pip in [('a','10.0.0.11'),('b','10.0.0.12'),('c','10.0.0.13')]:
        assert f'{peer}=https://{pip}:2380' in cmd
    for flag in ['--client-cert-auth=true','--peer-client-cert-auth=true','--trusted-ca-file=/run/etcd-tls/ca.crt','--peer-trusted-ca-file=/run/etcd-tls/ca.crt','--cert-file=/run/etcd-tls/member.crt','--key-file=/run/etcd-tls/member.key','--peer-cert-file=/run/etcd-tls/member.crt','--peer-key-file=/run/etcd-tls/member.key']:
        assert flag in cmd,flag
    assert p['etcd3']==dict(hosts='10.0.0.11:2379,10.0.0.12:2379,10.0.0.13:2379',protocol='https',cacert='/run/patroni-etcd-tls/ca.crt',cert='/run/patroni-etcd-tls/client.crt',key='/run/patroni-etcd-tls/client.key')
    for service,folder,target in [('etcd','etcd-tls','/run/etcd-tls'),('patroni','patroni-etcd-tls','/run/patroni-etcd-tls')]:
        mounts=[m for m in c['services'][service]['volumes'] if isinstance(m,dict)]
        assert mounts==[dict(type='bind',source='./'+folder,target=target,read_only=True,bind=dict(create_host_path=False))]
        assert os.stat(f'{root}/{n}/{folder}').st_mode & 0o777 == 0o700
        assert os.listdir(f'{root}/{n}/{folder}')==['README.txt']
        for other in c['services']:
            if other!=service:assert target not in str(c['services'][other].get('volumes',[]))
    env=c['services']['api']['environment']
    assert env['OAUTH_STATE_BACKEND']=='postgres'
    assert env['CACHE_BACKEND']==('off' if mode=='plain' else 'redis')
    assert 'redis' not in c['services']
PYETCD
 done
[ "$fails" = 0 ] && echo "ALL PASSED" || { echo "$fails FAILED"; exit 1; }
