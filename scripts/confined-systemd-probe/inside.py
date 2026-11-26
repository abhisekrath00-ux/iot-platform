#!/usr/bin/python3
"""Confined systemd feasibility only. Never executes application code."""
import json
import os
from pathlib import Path
import subprocess
import sys


def command(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=10)


def service_check():
    assert os.getuid() == 55000 and os.getgid() == 55000
    import grp
    assert grp.getgrnam("dialout").gr_gid in os.getgroups()
    status = Path('/proc/self/status').read_text()
    assert 'NoNewPrivs:\t1' in status
    # A world-writable outside path must be read-only due to ProtectSystem,
    # not ordinary permission refusal for this non-root account.
    try:
        Path('/usr/probe-write').write_text('wrong')
        raise AssertionError('ProtectSystem ineffective')
    except OSError as e:
        assert e.errno == 30, ('expected EROFS', e)
    try:
        Path('/home/probe-visible/sentinel').read_text()
        raise AssertionError('ProtectHome ineffective')
    except (PermissionError,FileNotFoundError):
        pass
    assert not Path('/tmp/host-sentinel').exists(), 'PrivateTmp ineffective'
    for parent in ['/etc/hexmon', '/var/lib/hexmon-edge']:
        p = Path(parent) / 'probe-write'
        p.write_text('allowed')
        p.unlink()
    Path('/tmp/service-private').write_text('private')
    # /proc/self/cgroup must show a real container service cgroup.
    assert 'hex-confined-probe.service' in Path('/proc/self/cgroup').read_text()
    print('PASS effective NNP/strict read-only system/home hidden/private tmp/RW exceptions/service cgroup')


def main():
    assert Path('/proc/1/comm').read_text().strip() == 'systemd'
    assert os.readlink('/proc/self/ns/net') != os.environ['PROBE_HOST_NETNS']
    assert os.readlink('/proc/self/ns/cgroup') != os.environ['PROBE_HOST_CGROUPNS']
    interfaces = json.loads(command('ip', '-j', 'link', 'show'))
    assert {x['ifname'] for x in interfaces} == {'lo'}, interfaces
    assert json.loads(command('ip', '-j', 'route', 'show', 'table', 'main')) == []
    command('ip', 'link', 'add', 'probe0', 'type', 'dummy')
    command('ip', 'addr', 'add', '10.203.0.1/32', 'dev', 'probe0')
    command('ip', 'link', 'set', 'probe0', 'up')
    assert {x['ifname'] for x in json.loads(command('ip', '-j', 'link'))} == {'lo', 'probe0'}
    assert json.loads(command('ip', '-j', 'route', 'show', 'table', 'main')) == []
    # Network unreachable, no attempted packet to a real destination.
    unreachable = subprocess.run(['ip', 'route', 'get', '192.0.2.1'], capture_output=True, text=True, timeout=5)
    assert unreachable.returncode != 0
    Path('/tmp/host-sentinel').write_text('outside service tmp')
    os.chmod('/usr', 0o777)  # Enable discriminating EROFS check; disposable image only.
    reference = Path('/probe/production-unit.reference').read_text()
    expected = ['NoNewPrivileges=true', 'ProtectSystem=strict', 'ProtectHome=true',
                'PrivateTmp=true', 'ReadWritePaths=/var/lib/hexmon-edge /etc/hexmon',
                'SupplementaryGroups=dialout']
    assert all(line in reference.splitlines() for line in expected)
    unit = '[Unit]\nDescription=Confined feasibility, no application\n[Service]\nType=oneshot\nUser=hexprobe\nGroup=hexprobe\n' + '\n'.join(expected) + '\nExecStart=/usr/bin/python3 /probe/inside.py service-check\n'
    path = Path('/run/systemd/system/hex-confined-probe.service')
    path.write_text(unit)
    command('systemctl', 'daemon-reload')
    command('systemctl', 'start', 'hex-confined-probe.service')
    props = command('systemctl', 'show', 'hex-confined-probe.service', '--property=Result,ExecMainStatus,NoNewPrivileges,ProtectSystem,ProtectHome,PrivateTmp,ReadWritePaths,SupplementaryGroups')
    pairs = dict(line.split('=', 1) for line in props.strip().splitlines())
    for k, v in {'Result':'success', 'ExecMainStatus':'0', 'NoNewPrivileges':'yes', 'ProtectSystem':'strict', 'ProtectHome':'yes', 'PrivateTmp':'yes', 'SupplementaryGroups':'dialout'}.items():
        assert pairs.get(k) == v, (k, pairs)
    assert set(pairs['ReadWritePaths'].split()) == {'/var/lib/hexmon-edge', '/etc/hexmon'}
    assert not Path('/tmp/service-private').exists()
    # No unknown/ignored/degraded property is accepted from manager strings alone.
    print(command('journalctl', '--unit=hex-confined-probe.service', '--no-pager', '-n', '30'))
    command('systemctl', 'stop', 'hex-confined-probe.service')
    path.unlink()
    command('systemctl', 'daemon-reload')
    assert 'LoadState=not-found' in command('systemctl', 'show', 'hex-confined-probe.service', '--property=LoadState')
    print(json.dumps({'status':'passed', 'application_started':False, 'interfaces':['lo','probe0'], 'routes':[], 'properties':pairs}))


if __name__ == '__main__':
    service_check() if sys.argv[1:] == ['service-check'] else main()
