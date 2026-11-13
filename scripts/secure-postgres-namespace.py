#!/usr/bin/env python3
"""Pinned Linux Docker network namespace for an explicit read-only probe.
No container start/exec, namespace creation, owner review or provider default.
Only the network namespace changes; host binary, mount/user/PID context stay host.
"""
import hashlib
import ipaddress
import json
import os
import pathlib
import re
import select
import stat
import subprocess

FIELDS={'container','image','started','project','service','network','network_id','address'}
class LinuxProcess:
    def __init__(self,pid,cid):
        self.pid,self.cid=pid,cid;self.ns=self.pidfd=None
        try:
            self.pidfd=os.pidfd_open(pid)
            before=self.snapshot()
            self.ns=os.open('/proc/'+str(pid)+'/ns/net',os.O_RDONLY|os.O_CLOEXEC)
            self.identity=self.snapshot()
            if before!=self.identity:raise ValueError('Process changed while pinning network namespace.')
            info=os.fstat(self.ns)
            if (info.st_dev,info.st_ino)!=tuple(self.identity['namespace']):raise ValueError('Pinned namespace differs from process.')
            self.check()
        except BaseException:self.close();raise
    def snapshot(self):
        base=pathlib.Path('/proc')/str(self.pid)
        raw=(base/'stat').read_text();lines=(base/'cgroup').read_text().splitlines()
        ns=os.stat(base/'ns/net');host=os.stat('/proc/self/ns/net')
        boot=pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()
        return self.parse(self.pid,self.cid,raw,lines,ns,host,boot)
    @staticmethod
    def parse(pid,cid,raw,lines,ns,host,boot):
        end=raw.rfind(') ')
        if end<0 or ' 'not in raw or raw[:raw.index(' ')]!=str(pid):raise ValueError('Exact process stat required.')
        fields=raw[end+2:].split()
        if len(fields)<20 or fields[0]in {'Z','X','x'}or not fields[19].isdigit():raise ValueError('Live process start ticks required.')
        # Linux rootful Docker cgroup-v1/v2. Other runtimes/layouts refuse.
        paths=[line.split(':',2)[2]for line in lines if len(line.split(':',2))==3]
        marker=re.compile(r'(?:^|/)(?:docker/'+cid+r'|docker-'+cid+r'\.scope)(?:/|$)')
        if not paths or len(paths)!=len(lines)or not all(marker.search(p)for p in paths):raise ValueError('Process cgroups do not bind exact Docker container.')
        if (ns.st_dev,ns.st_ino)==(host.st_dev,host.st_ino):raise ValueError('Host network namespace refused.')
        if not re.fullmatch(r'[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}',boot):raise ValueError('Kernel boot identity required.')
        return {'pid':pid,'ticks':fields[19],'cgroups':lines,'namespace':[ns.st_dev,ns.st_ino],'boot':boot}
    def check(self):
        if self.pidfd is None or self.ns is None:raise ValueError('Process namespace binding is closed.')
        poll=select.poll();poll.register(self.pidfd,select.POLLIN)
        if poll.poll(0)or self.snapshot()!=self.identity:raise ValueError('Pinned Docker process exited or changed.')
        ns=os.fstat(self.ns)
        if (ns.st_dev,ns.st_ino)!=tuple(self.identity['namespace']):raise ValueError('Pinned namespace descriptor changed.')
    def close(self):
        for name in ['ns','pidfd']:
            value=getattr(self,name,None)
            if value is not None:os.close(value);setattr(self,name,None)

class Namespace:
    SERVICES=frozenset({'api','ingest','mcp'})
    def __init__(self,tool,expected,guard,runner=None,process_factory=None):
        if (not isinstance(expected,dict)or set(expected)!=FIELDS or not callable(guard)
                or not re.fullmatch(r'[a-f0-9]{64}',expected.get('container',''))
                or not re.fullmatch(r'sha256:[a-f0-9]{64}',expected.get('image',''))
                or expected.get('service')not in self.SERVICES
                or not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',expected.get('project',''))
                or not isinstance(expected.get('started'),str)or not expected['started']
                or not isinstance(expected.get('network'),str)or not expected['network']
                or not re.fullmatch(r'[a-f0-9]{64}',expected.get('network_id',''))):
            raise ValueError('Exact accepted application container/start/image/network binding required.')
        ip=ipaddress.IPv4Address(expected['address'])
        if str(ip)!=expected['address']or not any(ip in ipaddress.IPv4Network(n)for n in ['10.0.0.0/8','172.16.0.0/12','192.168.0.0/16']):
            raise ValueError('Canonical private application source IPv4 required.')
        self.tool,self.expected,self.guard=tool,dict(expected),guard
        self.run=runner or subprocess.run;self.factory=process_factory or LinuxProcess;self.process=None
    def inspect(self):
        self.tool.check()
        if self.guard()is not None:raise ValueError('Raising fresh accepted-group guard required.')
        result=self.run(['docker','inspect',self.expected['container']],capture_output=True,text=True,check=True,timeout=10)
        values=json.loads(result.stdout)
        if not isinstance(values,list)or len(values)!=1:raise ValueError('One exact accepted namespace container required.')
        item=values[0];host=item.get('HostConfig',{});live=item.get('State',{});labels=item.get('Config',{}).get('Labels',{})
        net=item.get('NetworkSettings',{}).get('Networks',{}).get(self.expected['network'],{})
        if (item.get('Id')!=self.expected['container']or item.get('Image')!=self.expected['image']
                or live.get('StartedAt')!=self.expected['started']or live.get('Status')!='running'
                or live.get('Paused')is not False or live.get('Restarting')is not False
                or type(live.get('Pid'))is not int or live['Pid']<=1
                or host.get('Privileged')is not False or host.get('CapAdd')or host.get('Devices')
                or host.get('NetworkMode')in {'host','none'}or str(host.get('NetworkMode','')).startswith('container:')
                or host.get('PidMode')=='host' or str(host.get('PidMode','')).startswith('container:')
                or labels.get('com.docker.compose.project')!=self.expected['project']
                or labels.get('com.docker.compose.service')!=self.expected['service']
                or net.get('NetworkID')!=self.expected['network_id']or net.get('IPAddress')!=self.expected['address']):
            raise ValueError('Accepted container/process/network changed.')
        self.tool.check()
        return live['Pid']
    def bind(self):
        if self.process is not None:raise ValueError('Namespace binding is immutable per invocation.')
        pid=self.inspect();process=self.factory(pid,self.expected['container'])
        try:
            if self.inspect()!=pid:raise ValueError('Docker process changed while pinning namespace.')
            process.check();self.process=process
        except BaseException:process.close();raise
        return self
    def check(self):
        if self.process is None:raise ValueError('Pin exact accepted namespace first.')
        self.process.check()
        if self.inspect()!=self.process.pid:raise ValueError('Docker process identity changed.')
        self.process.check()
    def probe(self,request):
        # Request parsing remains owned by the bounded static setupctl. No
        # caller environment, passfile, DNS or secret command-line arguments.
        if not isinstance(request,dict):raise ValueError('Explicit connection object required.')
        encoded=json.dumps(request)
        if len(encoded.encode())>8192:raise ValueError('Bounded explicit connection request required.')
        self.check();binary=self.tool.ready();v=self.tool.record()
        try:fd=os.open(binary,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
        except OSError:raise ValueError('Private static probe unavailable.')from None
        try:
            info=os.fstat(fd)
            if (not stat.S_ISREG(info.st_mode)or stat.S_IMODE(info.st_mode)!=0o500 or info.st_uid!=os.geteuid()
                    or info.st_nlink!=1 or info.st_size>40*1024*1024):raise ValueError('Private static probe changed.')
            with os.fdopen(fd,'rb',closefd=False)as stream:raw=stream.read(40*1024*1024+1)
            if hashlib.sha256(raw).hexdigest()!=v['sha256']:raise ValueError('Pinned static probe bytes changed.')
            self.check()
            result=self.run(['/usr/bin/nsenter','--net=/proc/self/fd/'+str(self.process.ns),'--','/proc/self/fd/'+str(fd),'connection-readiness'],
                input=encoded,capture_output=True,text=True,check=True,timeout=10,
                env={'PATH':'/usr/bin:/bin','LANG':'C'},pass_fds=(self.process.ns,fd))
            self.check()
            if len(result.stdout)>4096:raise ValueError('Bounded host namespace proof required.')
            value=json.loads(result.stdout)
            if not isinstance(value,dict)or type(value.get('version'))is not int or value.get('version')!=1:raise ValueError('Exact connection proof required.')
            if value.get('status')=='connected':
                if (set(value)!={'version','status','database','user','server','client'}
                        or value['client']!=self.expected['address']or value['server']!=request.get('host')
                        or value['database']!=request.get('database')or value['user']!=request.get('user')):
                    raise ValueError('Connection proof differs from exact source/target request.')
            elif (set(value)!={'version','status'}or value.get('status')not in
                    {'hba_denied','database_closed','authentication_failed','unverified'}):
                raise ValueError('Exact bounded connection outcome required.')
            return value
        except (OSError,subprocess.SubprocessError,json.JSONDecodeError):
            raise ValueError('Host namespace connection proof unavailable or unverified.')from None
        finally:os.close(fd)
    def __enter__(self):return self.bind()
    def __exit__(self,*args):self.close()
    def close(self):
        if self.process is not None:self.process.close();self.process=None


class OutsideNamespace(Namespace):
    """Explicit existing web endpoint, never an admitted application writer.

    Separate type so normal writer Namespace does not accept web as a writer.
    Exact reviewed CID/image/start/network and raising live guard still required.
    No endpoint discovery or new container is performed here.
    """
    SERVICES=frozenset({'web'})
