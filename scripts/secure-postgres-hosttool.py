#!/usr/bin/env python3
"""Private exact-image static probe staging, not a provider/menu entry.

Never starts the source container. No password is staged and no namespace or
connection is chosen here. Explicit journal scope and live guard stay mandatory.
Interrupted stages require resume on a fresh instance; cleanup is explicit.
"""
import hashlib
import importlib.util
import io
import json
import os
import pathlib
import re
import stat
import struct
import subprocess
import tarfile
import uuid

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('host_tool_state',HERE/'secure-state.py')
state=importlib.util.module_from_spec(spec);spec.loader.exec_module(state)

class HostTool:
    def __init__(self,journal,image,guard,runner=None):
        if not re.fullmatch(state.IMAGE,image)or not callable(guard):raise ValueError('Exact candidate image and live admission guard required.')
        self.journal,self.image,self.guard=journal,image,guard
        self.run=runner or subprocess.run
        self.path=journal.directory/'postgres-hosttool.json'

    def check(self):
        self.journal.locked()
        binding=state.read_private(self.journal.directory/'maintenance.json')
        if binding.get('root')!=str(self.journal.root)or binding.get('scope')!='application_bootstrap' or self.guard()is not None:
            raise ValueError('Exact held application admission required for host probe staging.')
        return binding

    def command(self,args,binary=False):
        self.check()
        try:
            result=self.run(['docker',*args],capture_output=True,text=not binary,timeout=20,check=True)
        except (subprocess.SubprocessError,OSError):raise ValueError('Exact host probe source unavailable; retain staging evidence.')from None
        self.check();return result.stdout

    def record(self):
        self.check();v=state.read_private(self.path)
        if (set(v)!={'version','binding','image','source','cid','stage','sha256','binary'}or v['version']!=1
                or v['binding']!=self.check()or v['image']!=self.image
                or not re.fullmatch(r'hexthings-hostprobe-[a-f0-9]{32}',v.get('source',''))
                or not isinstance(v.get('cid'),str)or (v['cid']and not re.fullmatch(r'[a-f0-9]{64}',v['cid']))
                or (v['stage']!='creating'and not v['cid'])
                or v['stage']not in {'creating','copying','ready','released'}
                or v['binary']!=str(self.journal.directory/(v['source']+'.bin'))
                or (v['stage']in {'copying','ready','released'}and not re.fullmatch(r'[a-f0-9]{64}',v.get('sha256','')))):
            raise ValueError('Exact durable host probe staging record required.')
        return v

    def source(self,v,missing=False):
        ids=self.command(['container','ls','-a','--no-trunc','--filter','name=^/'+v['source']+'$','--format','{{.ID}}']).splitlines()
        if not ids and missing:return None
        if len(ids)!=1 or not re.fullmatch(r'[a-f0-9]{64}',ids[0]):raise ValueError('One exact never-started host probe source required.')
        items=json.loads(self.command(['inspect',ids[0]]))
        if (not isinstance(items,list)or len(items)!=1):raise ValueError('Exact source inspection required.')
        item=items[0]
        if (item.get('Id')!=ids[0]or (v['cid']and ids[0]!=v['cid'])or item.get('Name')!='/'+v['source']or item.get('Image')!=self.image
                or item.get('State',{}).get('Status')!='created'
                or item.get('HostConfig',{}).get('NetworkMode')!='none'
                or item.get('HostConfig',{}).get('Privileged')is not False
                or item.get('Mounts')!=[] or item.get('Config',{}).get('Entrypoint')!=['/bin/true']
                or item.get('Config',{}).get('Labels',{}).get('hexthings.hostprobe')!=v['source']):
            raise ValueError('Host probe source identity changed.')
        return ids[0]

    @staticmethod
    def unpack(raw):
        if not isinstance(raw,bytes)or len(raw)>50*1024*1024:raise ValueError('Bounded static probe archive required.')
        try:
            with tarfile.open(fileobj=io.BytesIO(raw),mode='r:')as tar:
                entries=tar.getmembers()
                if len(entries)!=1 or entries[0].name!='setupctl'or not entries[0].isfile()or entries[0].size>40*1024*1024:
                    raise ValueError('One regular exact static probe binary required.')
                content=tar.extractfile(entries[0]).read()
        except (tarfile.TarError,OSError):raise ValueError('Static probe archive invalid.')from None
        if len(content)<64 or content[:4]!=b'\x7fELF' or content[4:6]!=b'\x02\x01' or content[18:20]!=b'\x3e\x00':
            raise ValueError('Only exact Linux amd64 ELF probe supported.')
        # Static Linux amd64 only. Reject an interpreter and dynamic loader
        # dependencies, rather than merely relying on the image build flags.
        kind,version=struct.unpack_from('<H',content,16)[0],struct.unpack_from('<I',content,20)[0]
        offset=struct.unpack_from('<Q',content,32)[0]
        ehsize,entrysize,count=struct.unpack_from('<HHH',content,52)
        if (kind not in {2,3}or version!=1 or content[6]!=1 or content[7]not in {0,3}
                or ehsize!=64 or entrysize!=56 or not 0<count<=1024 or offset<64
                or offset+count*entrysize>len(content)):
            raise ValueError('Valid bounded static ELF program headers required.')
        load=False
        for n in range(count):
            kind=struct.unpack_from('<I',content,offset+n*entrysize)[0]
            if kind in {2,3}:raise ValueError('Dynamic or interpreted probe refused.')
            if kind==1:
                start,size=struct.unpack_from('<Q',content,offset+n*entrysize+8)[0],struct.unpack_from('<Q',content,offset+n*entrysize+32)[0]
                if start+size>len(content):raise ValueError('ELF load segment outside binary.')
                load=True
        if not load:raise ValueError('Static ELF load segment required.')
        return content

    def binary(self,v):
        p=pathlib.Path(v['binary']);fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
        try:
            info=os.fstat(fd)
            if (not stat.S_ISREG(info.st_mode)or stat.S_IMODE(info.st_mode)!=0o500
                    or info.st_uid!=os.geteuid()or info.st_nlink!=1 or info.st_size>40*1024*1024):
                raise ValueError('Exact private owned single-link host probe binary required.')
            with os.fdopen(fd,'rb',closefd=False)as f:raw=f.read(40*1024*1024+1)
            if hashlib.sha256(raw).hexdigest()!=v['sha256']:raise ValueError('Host probe binary changed.')
            return p
        finally:os.close(fd)

    def prepare(self):
        self.check()
        if self.path.exists()or self.path.is_symlink():raise ValueError('Existing host tool needs explicit fresh resume or release.')
        source='hexthings-hostprobe-'+uuid.uuid4().hex
        v={'version':1,'binding':self.check(),'image':self.image,'source':source,'cid':'','stage':'creating','sha256':'',
           'binary':str(self.journal.directory/(source+'.bin'))}
        state.atomic(self.path,v)
        return self.resume()

    def resume(self):
        v=self.record()
        if v['stage']=='released':raise ValueError('Released probe cannot be silently recreated.')
        if v['stage']=='creating':
            if self.source(v,True)is None:
                self.command(['create','--name',v['source'],'--network','none','--label','hexthings.hostprobe='+v['source'],'--entrypoint','/bin/true',self.image])
            cid=self.source(v)
            raw=self.unpack(self.command(['cp',cid+':/bin/setupctl','-'],True))
            v['cid']=cid;v['sha256']=hashlib.sha256(raw).hexdigest();v['stage']='copying';state.atomic(self.path,v)
        if v['stage']=='copying':
            cid=self.source(v);raw=self.unpack(self.command(['cp',cid+':/bin/setupctl','-'],True))
            if hashlib.sha256(raw).hexdigest()!=v['sha256']:raise ValueError('Exact immutable probe source bytes changed.')
            p=pathlib.Path(v['binary'])
            if p.exists()or p.is_symlink():self.binary(v)
            else:
                fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o500)
                with os.fdopen(fd,'wb')as f:f.write(raw);f.flush();os.fsync(f.fileno())
                state.sync_directory(p.parent)
            self.binary(v);v['stage']='ready';state.atomic(self.path,v)
        self.binary(v)
        cid=self.source(v,True)
        if cid is not None:self.command(['rm',cid])
        self.check();return self.binary(v)

    def ready(self):
        v=self.record()
        if v['stage']!='ready'or self.source(v,True)is not None:raise ValueError('Exact host probe is not settled ready.')
        return self.binary(v)

    def release(self):
        v=self.record()
        if v['stage']=='released':
            if pathlib.Path(v['binary']).exists()or pathlib.Path(v['binary']).is_symlink():raise ValueError('Released binary unexpectedly exists.')
            return
        if v['stage']!='ready'or self.source(v,True)is not None:raise ValueError('Resume checked staging before release.')
        p=pathlib.Path(v['binary'])
        if p.exists()or p.is_symlink():self.binary(v);p.unlink();state.sync_directory(p.parent)
        v['stage']='released';state.atomic(self.path,v);self.check()
