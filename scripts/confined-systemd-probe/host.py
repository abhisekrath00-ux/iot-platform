#!/usr/bin/env python3
"""Review-only proposal. Exact owned Podman container, no app or host services."""
import argparse
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import time
import uuid

CAPS = ['CHOWN','DAC_OVERRIDE','FOWNER','FSETID','KILL','SETGID','SETUID',
        'SETPCAP','SETFCAP','SYS_CHROOT','SYS_ADMIN','MKNOD','NET_ADMIN']


def call(*args, timeout=20):
    return subprocess.check_output(['podman', *args], text=True, stderr=subprocess.STDOUT, timeout=timeout)


def save(d, state):
    p = d / 'owner.pending.json'
    with p.open('w') as f:
        json.dump(state, f, indent=2); f.flush(); os.fsync(f.fileno())
    p.replace(d / 'owner.json')
    fd=os.open(d,os.O_DIRECTORY)
    try: os.fsync(fd)
    finally: os.close(fd)


def image_id(value):
    # Only two full SHA256 spellings. No short IDs, tags, whitespace or coercion.
    assert isinstance(value, str) and re.fullmatch(r'(?:sha256:)?[0-9a-f]{64}', value), ('malformed image ID', value)
    return 'sha256:' + value.removeprefix('sha256:')


def container_id(value):
    assert isinstance(value, str) and re.fullmatch(r'[0-9a-f]{64}', value), ('malformed container ID', value)
    return value


def audit_ids(kind, **values):
    print(json.dumps({'identity_check': kind, **values}), flush=True)


def diagnostic(kind, value):
    # One JSON line cannot create runner commands or terminal control sequences.
    text = str(value)
    text = re.sub(r'(?i)(authorization|password|passwd|secret|token|api[_-]?key)([\s:=]+)[^\s,;]+', r'\1\2[redacted]', text)
    text = re.sub(r'(?i)(https?://)[^/@\s]+:[^/@\s]+@', r'\1[redacted]@', text)
    print(json.dumps({'diagnostic': kind, 'text': text[:8192], 'truncated': len(text) > 8192}, ensure_ascii=True), flush=True)


def inspect_summary(x):
    # Deliberately omit environment, labels, host paths, command and config.
    h = x.get('HostConfig', {})
    state = x.get('State', {})
    return {'Id': x.get('Id'), 'Image': x.get('Image'),
            'State': {k: state.get(k) for k in ('Status', 'Running', 'ExitCode', 'Error', 'OOMKilled', 'Pid')},
            'HostConfig': {k: h.get(k) for k in ('Privileged', 'NetworkMode', 'CgroupMode', 'PidMode', 'Cgroups', 'SecurityOpt', 'PidsLimit', 'Memory', 'CpuPeriod', 'CpuQuota')},
            'LogConfig': {k: h.get('LogConfig', {}).get(k) for k in ('Type', 'Size')},
            'EffectiveCaps': x.get('EffectiveCaps'),
            'MountCount': len(x.get('Mounts') or []),
            'BindCount': len(h.get('Binds') or []),
            'PortBindingCount': len(h.get('PortBindings') or {}),
            'Tty': x.get('Config', {}).get('Tty')}


def best_effort(kind, get_value):
    try:
        diagnostic(kind, get_value())
    except Exception as e:
        # Error class only: a diagnostic failure must not mask the probe error.
        try: diagnostic(kind + '-unavailable', type(e).__name__)
        except Exception: pass


def validate_inspect(x):
    assert x['Config']['Tty'] is True, 'private pseudo-TTY required for diagnostic console'
    h = x['HostConfig']
    assert h['Privileged'] is False and h['NetworkMode'] == 'none'
    # Podman4.9.3 reports CgroupMode, not Docker's CgroupnsMode.
    assert h['CgroupMode'] == 'private' and h['PidMode'] == 'private'
    assert h['Cgroups'] == 'default'
    assert h['PortBindings'] in ({}, None) and h['Binds'] in ([], None)
    assert x['Mounts'] in ([], None), 'no externally supplied volumes/binds'
    security = h['SecurityOpt']
    assert security is None or (isinstance(security, list) and all(isinstance(v, str) and 'unconfined' not in v for v in security))
    caps = x['EffectiveCaps']
    assert isinstance(caps, list) and all(isinstance(v, str) and re.fullmatch(r'CAP_[A-Z0-9_]+', v) for v in caps)
    assert len(caps) == len(set(caps)) and {v.removeprefix('CAP_') for v in caps} == set(CAPS), ('unexpected capability set', caps)
    assert h['PidsLimit'] == 128 and h['Memory'] == 768 * 1024 * 1024
    assert h['CpuPeriod'] == 100000 and h['CpuQuota'] == 200000
    assert h['LogConfig']['Type'] == 'k8s-file'


def inspect_owned(state):
    # Exact predeclared unique name; immutable ID captured at first observation.
    result = subprocess.run(['podman','container','exists',state['name']], timeout=10)
    if result.returncode == 1: return None
    if result.returncode != 0: raise RuntimeError('container existence unavailable')
    data = json.loads(call('container','inspect',state['name']))
    assert isinstance(data, list) and len(data) == 1 and isinstance(data[0], dict)
    x = data[0]
    audit_ids('container', expected_image=state['image'], observed_image=x['Image'], expected_container=state.get('id'), observed_container=x['Id'])
    container_id(x['Id'])
    assert x['Config']['Labels'].get('hex.confined.owner') == state['token']
    assert isinstance(x['State']['Running'], bool), 'unknown running state'
    assert image_id(x['Image']) == image_id(state['image']), 'owned image mismatch'
    if state.get('id'): assert container_id(x['Id']) == container_id(state['id']), 'owned container mismatch'
    return x


def cleanup(d):
    state = json.loads((d/'owner.json').read_text())
    x=inspect_owned(state)
    if x:
        state['id']=x['Id'];save(d,state)
        call('stop','--time','5',x['Id'],timeout=15)
        x=inspect_owned(state);assert x['State']['Running'] is False
        call('rm',x['Id'])
    assert inspect_owned(state) is None
    state['cleanup']='exact container absent';save(d,state)
    print('Exact labeled container stopped/removed/absent, not application teardown acceptance')


def run(d):
    raw_image = Path(os.environ['PROBE_IMAGE_ID_FILE']).read_text()
    # IID file from Buildah has no newline; inspect formatter has one newline.
    image = image_id(raw_image)
    raw_inspected = call('image','inspect',image,'--format','{{.Id}}')
    observed = raw_inspected.removesuffix('\n')
    evidence = {'iid_file': raw_image, 'inspect_stdout': raw_inspected}
    (d/'image-identity.json').write_text(json.dumps(evidence, indent=2))
    audit_ids('image', **evidence)
    assert image_id(observed) == image, 'built image mismatch'
    state={'name':'hex-confined-probe-'+uuid.uuid4().hex,'token':uuid.uuid4().hex,'image':image,'image_identity':evidence}
    save(d,state)  # Before container creation; cleanup finds only matching random label+image.
    args=['create','--tty','--name',state['name'],'--label','hex.confined.owner='+state['token'],
          '--pull=never','--network=none','--cgroupns=private','--pid=private',
          '--systemd=always','--cap-drop=all']
    for cap in CAPS:args.append('--cap-add='+cap)
    args += ['--pids-limit=128','--memory=768m','--cpus=2','--log-driver=k8s-file',
             '--log-opt=max-size=1048576','--env','PROBE_HOST_NETNS='+os.readlink('/proc/self/ns/net'),
             '--env','PROBE_HOST_CGROUPNS='+os.readlink('/proc/self/ns/cgroup'),image]
    readiness = None
    try:
        state['id']=container_id(call(*args).removesuffix('\n'));save(d,state)
        x=inspect_owned(state)
        (d/'inspect-before-start.json').write_text(json.dumps(x, indent=2))
        best_effort('inspect-before-start', lambda: inspect_summary(x))
        validate_inspect(x)
        save(d,{**state,'inspect_before_start':x})
        call('start',state['id'])
        # Readiness check only, no host systemctl.
        deadline=time.monotonic()+30
        while True:
            try:
                p=subprocess.run(['podman','exec',state['id'],'systemctl','show','--property=Version'],capture_output=True,text=True,timeout=5)
            except subprocess.TimeoutExpired as e:
                readiness = {'error': 'TimeoutExpired', 'stdout': e.stdout, 'stderr': e.stderr}
                raise
            readiness = {'returncode': p.returncode, 'stdout': p.stdout, 'stderr': p.stderr}
            if p.returncode==0:break
            if time.monotonic()>deadline:raise RuntimeError('container systemd unavailable')
            time.sleep(.2)
        output=call('exec',state['id'],'python3','/probe/inside.py',timeout=60)
        (d/'probe.log').write_text(output)
        state['result']='passed';save(d,state)
    except BaseException as e:
        best_effort('failed-readiness', lambda: readiness)
        state['result']='failed';state['error']=type(e).__name__
        try: save(d,state)
        except Exception: pass
        raise
    finally:
        primary_error = sys.exc_info()[1]
        # Emit before exact cleanup destroys the evidence. Each read is bounded.
        if primary_error is not None:
            best_effort('inspect-after-failure', lambda: inspect_summary(inspect_owned(state) or {}))
        def container_logs():
            output = call('logs','--tail','100',state['name'])[-65536:]
            try: (d/'container.log').write_text(output)
            except Exception: pass
            return output
        best_effort('container-logs', container_logs)
        try:
            cleanup(d)
        except BaseException as cleanup_error:
            best_effort('cleanup-failed', lambda: type(cleanup_error).__name__)
            if primary_error is None: raise
            # The independent workflow cleanup remains required on this path.



if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('action',choices=['run','cleanup']);p.add_argument('directory');a=p.parse_args()
    d=Path(a.directory).resolve(strict=True);s=d.stat()
    assert s.st_uid==0 and s.st_mode&0o077==0
    if a.action=='run':
        assert not (d/'owner.json').exists()
        run(d)
    else:cleanup(d)
