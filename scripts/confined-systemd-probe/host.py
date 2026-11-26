#!/usr/bin/env python3
"""Review-only proposal. Exact owned Podman container, no app or host services."""
import argparse
import json
import os
from pathlib import Path
import subprocess
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


def inspect_owned(state):
    # Exact predeclared unique name; immutable ID captured at first observation.
    result = subprocess.run(['podman','container','exists',state['name']], timeout=10)
    if result.returncode == 1: return None
    if result.returncode != 0: raise RuntimeError('container existence unavailable')
    x = json.loads(call('inspect',state['name']))[0]
    assert x['Config']['Labels'].get('hex.confined.owner') == state['token']
    assert x['Image'] == state['image']
    if state.get('id'): assert x['Id'] == state['id']
    return x


def cleanup(d):
    state = json.loads((d/'owner.json').read_text())
    x=inspect_owned(state)
    if x:
        state['id']=x['Id'];save(d,state)
        call('stop','--time','5',x['Id'],timeout=15)
        x=inspect_owned(state);assert not x['State']['Running']
        call('rm',x['Id'])
    assert inspect_owned(state) is None
    state['cleanup']='exact container absent';save(d,state)
    print('Exact labeled container stopped/removed/absent, not application teardown acceptance')


def run(d):
    image=Path(os.environ['PROBE_IMAGE_ID_FILE']).read_text().strip()
    assert image.startswith('sha256:') and len(image)==71
    assert call('image','inspect',image,'--format','{{.Id}}').strip()==image
    state={'name':'hex-confined-probe-'+uuid.uuid4().hex,'token':uuid.uuid4().hex,'image':image}
    save(d,state)  # Before container creation; cleanup finds only matching random label+image.
    args=['create','--name',state['name'],'--label','hex.confined.owner='+state['token'],
          '--pull=never','--network=none','--cgroupns=private','--pid=private',
          '--systemd=always','--cap-drop=all']
    for cap in CAPS:args.append('--cap-add='+cap)
    args += ['--pids-limit=128','--memory=768m','--cpus=2','--log-driver=k8s-file',
             '--log-opt=max-size=1048576','--env','PROBE_HOST_NETNS='+os.readlink('/proc/self/ns/net'),
             '--env','PROBE_HOST_CGROUPNS='+os.readlink('/proc/self/ns/cgroup'),image]
    try:
        state['id']=call(*args).strip();save(d,state)
        x=inspect_owned(state);h=x['HostConfig']
        assert not h['Privileged'] and h['NetworkMode']=='none' and h['CgroupnsMode']=='private'
        assert not h.get('PortBindings') and not h.get('Binds')
        assert not x.get('Mounts'), 'no externally supplied volumes/binds'
        assert not any('unconfined' in v for v in h.get('SecurityOpt',[]))
        actual={cap.removeprefix('CAP_') for cap in x['EffectiveCaps']}
        assert actual==set(CAPS), ('unexpected capability set',actual)
        assert h.get('PidMode')=='private'
        assert h.get('CgroupMode','private')=='private' 
        save(d,{**state,'inspect_before_start':x})
        call('start',state['id'])
        # Readiness check only, no host systemctl.
        deadline=time.monotonic()+30
        while True:
            p=subprocess.run(['podman','exec',state['id'],'systemctl','show','--property=Version'],capture_output=True,text=True,timeout=5)
            if p.returncode==0:break
            if time.monotonic()>deadline:raise RuntimeError('container systemd unavailable')
            time.sleep(.2)
        output=call('exec',state['id'],'python3','/probe/inside.py',timeout=60)
        (d/'probe.log').write_text(output)
        state['result']='passed';save(d,state)
    except BaseException as e:
        state['result']='failed';state['error']=type(e).__name__;save(d,state)
        raise
    finally:
        # Bounded logs even after readiness/probe failure; always-step repeats cleanup.
        try:(d/'container.log').write_text(call('logs','--tail','100',state['name'])[-65536:])
        finally:cleanup(d)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('action',choices=['run','cleanup']);p.add_argument('directory');a=p.parse_args()
    d=Path(a.directory).resolve(strict=True);s=d.stat()
    assert s.st_uid==0 and s.st_mode&0o077==0
    if a.action=='run':
        assert not (d/'owner.json').exists()
        run(d)
    else:cleanup(d)
