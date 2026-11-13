#!/usr/bin/env python3
"""Run exact candidate setupctl through the fenced PostgreSQL local socket.

Internal application adapter callback only. No default target, network fallback,
new credential, owner approval or automatic cleanup of interrupted tool state.
Uses a never-started image container to read the exact static binary/migrations,
then a private durable target-container directory. Checks surround every effect.
The host caller owns admission, HBA, host inventory, scope and socket proof.
"""
import hashlib
import importlib.util
import io
import json
import pathlib
import re
import subprocess
import tarfile
import uuid
from urllib.parse import urlencode

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('operator_state',HERE/'secure-state.py')
state=importlib.util.module_from_spec(spec);spec.loader.exec_module(state)
appspec=importlib.util.spec_from_file_location('local_operator_application',HERE/'secure-application.py')
application=importlib.util.module_from_spec(appspec);appspec.loader.exec_module(application)


class LocalOperator:
    def __init__(self,journal,catalog,image,guard,runner=None):
        if not re.fullmatch(r'sha256:[a-f0-9]{64}',image):raise ValueError('Exact candidate image required.')
        if not callable(guard):raise ValueError('Live controlled-channel guard required.')
        if not re.fullmatch(r'[a-f0-9]{64}',catalog.container):raise ValueError('Exact PostgreSQL container required.')
        if not re.fullmatch(r'[a-zA-Z_][a-zA-Z0-9_]{0,62}',catalog.user) or not re.fullmatch(r'[a-zA-Z_][a-zA-Z0-9_]{0,62}',catalog.database):
            raise ValueError('Simple exact local database/admin names required.')
        self.journal,self.catalog,self.image,self.guard=journal,catalog,image,guard
        self.run=runner or subprocess.run
        self.path=journal.directory/'postgres-operator.json'

    def check(self):
        self.journal.locked()
        value=state.read_private(self.journal.directory/'maintenance.json')
        if value.get('root')!=str(self.journal.root):raise ValueError('Held matching admission required for schema tools.')
        if self.guard()is not None:raise ValueError('Controlled-channel guard must raise, not return approval.')

    def command(self,args,input=None,text=True):
        self.check()
        try:result=self.run(['docker',*args],capture_output=True,text=text,input=input,timeout=150,check=True)
        except (subprocess.SubprocessError,OSError):raise ValueError('Controlled local schema tool failed; keep maintenance and tool evidence.')from None
        self.check()
        return result.stdout

    def record(self):
        value=state.read_private(self.path)
        expected={'version','image','container','user','database','binding','source','directory','stage','archive_sha256','files'}
        if (set(value)!=expected or value['version']!=1 or value['image']!=self.image or value['container']!=self.catalog.container
                or value['user']!=self.catalog.user or value['database']!=self.catalog.database
                or value['binding']!=state.read_private(self.journal.directory/'maintenance.json')
                or not re.fullmatch(r'hexthings-schema-[a-f0-9]{32}',value['source'])
                or value['directory']!='/tmp/'+value['source']
                or value['stage']not in {'creating','copying','ready','removed'}):
            raise ValueError('Exact interrupted schema-tool record required.')
        return value

    @staticmethod
    def archive(binary,migrations):
        files={}
        for raw,is_binary in [(binary,True),(migrations,False)]:
            if not isinstance(raw,bytes)or len(raw)>80*1024*1024:raise ValueError('Bounded candidate tool archive required.')
            try:
                with tarfile.open(fileobj=io.BytesIO(raw),mode='r:*')as tar:
                    for member in tar:
                        parts=pathlib.PurePosixPath(member.name).parts
                        if member.isdir()and not is_binary and member.name.rstrip('/')=='migrations':continue
                        good=(member.name=='setupctl'if is_binary else len(parts)==2 and parts[0]=='migrations'and re.fullmatch(r'[0-9]{4}_[a-zA-Z0-9_-]+\.sql',parts[1]))
                        if not good or not member.isfile()or member.name in files or member.size>50*1024*1024:
                            raise ValueError('Candidate tool archive contains unsupported paths/types.')
                        content=tar.extractfile(member).read()
                        if is_binary and not content.startswith(b'\x7fELF'):raise ValueError('Candidate local operator must be an ELF executable.')
                        files[member.name]=content
            except (tarfile.TarError,OSError):raise ValueError('Candidate tool archive unavailable.')from None
        if 'setupctl'not in files or len(files)<2:raise ValueError('Exact candidate executable and migration catalog required.')
        out=io.BytesIO()
        with tarfile.open(fileobj=out,mode='w')as tar:
            for name,raw in sorted(files.items()):
                item=tarfile.TarInfo(name);item.size=len(raw);item.mode=0o500 if name=='setupctl'else 0o400
                item.uid=item.gid=0;tar.addfile(item,io.BytesIO(raw))
        return out.getvalue(),{key:hashlib.sha256(raw).hexdigest()for key,raw in files.items()}

    def source(self,value):
        items=json.loads(self.command(['inspect',value['source']]))
        if (len(items)!=1 or items[0].get('Name')!='/'+value['source']or items[0].get('Image')!=self.image
                or items[0].get('State',{}).get('Status')!='created'
                or items[0].get('HostConfig',{}).get('NetworkMode')!='none'
                or items[0].get('Config',{}).get('Labels',{}).get('hexthings.schema-tool')!=value['source']):
            raise ValueError('Temporary candidate tool source identity changed.')

    def prepare(self):
        self.check()
        if self.path.exists()or self.path.is_symlink():raise ValueError('Existing schema-tool state requires explicit checked cleanup first.')
        source='hexthings-schema-'+uuid.uuid4().hex
        value={'version':1,'image':self.image,'container':self.catalog.container,'user':self.catalog.user,'database':self.catalog.database,
               'binding':state.read_private(self.journal.directory/'maintenance.json'),'source':source,'directory':'/tmp/'+source,
               'stage':'creating','archive_sha256':'','files':{}}
        state.atomic(self.path,value)
        self.command(['create','--name',source,'--network','none','--label','hexthings.schema-tool='+source,'--entrypoint','/bin/true',self.image])
        self.source(value)
        binary=self.command(['cp',source+':/bin/setupctl','-'],text=False)
        migrations=self.command(['cp',source+':/migrations','-'],text=False)
        archive,manifest=self.archive(binary,migrations)
        value['files']=manifest;value['stage']='copying';value['archive_sha256']=hashlib.sha256(archive).hexdigest();state.atomic(self.path,value)
        # mkdir refuses an already-existing target rather than adopting it.
        self.command(['exec','--user','0',self.catalog.container,'mkdir','-m','700',value['directory']])
        self.command(['cp','-',self.catalog.container+':'+value['directory']],input=archive,text=False)
        self.source(value)
        self.command(['rm',value['source']])
        value['stage']='ready';state.atomic(self.path,value)

    def __call__(self,value,action):
        if action not in {'inspect-reviewed','apply-reviewed'}:raise ValueError('Explicit reviewed schema operation required.')
        if action=='apply-reviewed':
            if value.get('schema_attempted')!=application.ADDED:raise ValueError('Durable exact reviewed schema attempt required.')
            application.transport.check_backup(value.get('backup',{}))
        self.check();record=self.record()
        if record['stage']!='ready':raise ValueError('Candidate local schema tool is not ready; explicit recovery required.')
        directory=record['directory']
        if not isinstance(record['files'],dict)or 'setupctl'not in record['files']or len(record['files'])<2:
            raise ValueError('Exact staged operator manifest required.')
        for name,want in record['files'].items():
            if not (name=='setupctl'or re.fullmatch(r'migrations/[0-9]{4}_[a-zA-Z0-9_-]+\.sql',name))or not re.fullmatch(r'[a-f0-9]{64}',want):
                raise ValueError('Invalid staged operator manifest.')
        names=sorted(record['files'])
        output=self.command(['exec','--user','0',self.catalog.container,'sha256sum',*[directory+'/'+name for name in names]])
        expected=[record['files'][name]+'  '+directory+'/'+name for name in names]
        if output.splitlines()!=expected:raise ValueError('Staged candidate operator/catalog bytes changed.')
        # Resolve the server's exact active socket list. Multiple/custom/abstract
        # sockets require separate support, never a guessed /tmp or TCP fallback.
        socket=self.catalog.sql("SELECT json_build_object('sockets',current_setting('unix_socket_directories'))")
        path=socket.get('sockets')
        if not isinstance(path,str)or not path.startswith('/')or ','in path or pathlib.PurePosixPath(path).as_posix()!=path or '..'in pathlib.PurePosixPath(path).parts:
            raise ValueError('One canonical active local PostgreSQL socket directory required.')
        dsn='postgres:///'+self.catalog.database+'?'+urlencode({'host':path,'user':self.catalog.user,'connect_timeout':'5'})
        acknowledgement=None
        if action=='apply-reviewed':
            acknowledgement=json.dumps({'version':1,'scope':'application_bootstrap','writers_stopped':True,'backup_verified':True})
        output=self.command(['exec','-i','--user','0',self.catalog.container,'env','-i','DATABASE_URL='+dsn,'MIGRATIONS_DIR='+directory+'/migrations',
                             directory+'/setupctl',action],input=acknowledgement)
        return json.loads(output)

    def connection(self):
        """Read-only exact local-socket proof with staged candidate executable.

        Returns narrow setupctl outcomes, never treats a refusal as success.
        A transition-safe guard must be supplied by this invocation's caller;
        normal provider.guard is intentionally inappropriate while closing.
        """
        self.check();record=self.record()
        if record['stage']!='ready':raise ValueError('Exact staged candidate probe required.')
        binary=record['directory']+'/setupctl';want=record.get('files',{}).get('setupctl')
        if not isinstance(want,str)or not re.fullmatch(r'[a-f0-9]{64}',want):raise ValueError('Exact candidate probe digest required.')
        if self.command(['exec','--user','0',self.catalog.container,'sha256sum',binary]).splitlines()!=[want+'  '+binary]:
            raise ValueError('Staged candidate local probe bytes changed.')
        settings=self.catalog.sql("SELECT json_build_object('socket',current_setting('unix_socket_directories'),'port',current_setting('port'))")
        path=settings.get('socket');port=settings.get('port')
        if (not isinstance(path,str)or not path.startswith('/')or ','in path
                or pathlib.PurePosixPath(path).as_posix()!=path or '..'in pathlib.PurePosixPath(path).parts
                or not isinstance(port,str)or not port.isdigit()or not 0<int(port)<65536):
            raise ValueError('One exact active local socket/port required.')
        request={'version':1,'transport':'local_socket','host':path,'port':int(port),
                 'database':self.catalog.database,'user':self.catalog.user,'password':''}
        raw=self.command(['exec','-i','--user','0',self.catalog.container,'env','-i',binary,'connection-readiness'],input=json.dumps(request))
        if len(raw)>4096:raise ValueError('Bounded local socket outcome required.')
        try:result=json.loads(raw)
        except ValueError:raise ValueError('Exact local socket outcome unavailable.')from None
        if not isinstance(result,dict)or type(result.get('version'))is not int or result['version']!=1:
            raise ValueError('Exact local socket outcome required.')
        if result.get('status')=='connected':
            if set(result)!={'version','status','database','user'}or result['database']!=self.catalog.database or result['user']!=self.catalog.user:
                raise ValueError('Exact local socket database/admin required.')
        elif set(result)!={'version','status'}or result.get('status')not in {'hba_denied','database_closed','authentication_failed','unverified'}:
            raise ValueError('Exact bounded local socket outcome required.')
        return result

    def recover_staging(self):
        """Explicitly discard only verified partial tool artifacts, never SQL/data.

        No schema operation can have run in creating/copying. Unknown files,
        altered bytes, a started source container or changed target fail closed.
        The generated UUID namespace is reserved for these transient tools.
        """
        self.check();value=self.record()
        if value['stage']not in {'creating','copying','removed'}:
            raise ValueError('Only pre-SQL partial staging can be reset here; inspect ready tools first.')
        archived=self.journal.directory/(value['source']+'-discarded.json')
        if archived.exists()or archived.is_symlink():raise ValueError('Tool recovery evidence already exists.')
        if value['stage']=='removed':
            self.path.rename(archived);state.sync_directory(self.journal.directory);return
        source=self.command(['container','ls','-a','--filter','name=^/'+value['source']+'$','--format','{{.ID}}']).strip()
        if source:self.source(value)
        # Static read-only shell, exact generated path passed as an argument.
        # It never interpolates source data into shell code.
        script='if [ -L "$1" ]; then exit 1; fi; if [ -e "$1" ]; then tar -C "$1" -cf - .; else printf ABSENT; fi'
        raw=self.command(['exec','--user','0',self.catalog.container,'sh','-c',script,'hexthings-tool-check',value['directory']],text=False)
        if len(raw)>80*1024*1024:raise ValueError('Partial tool archive is too large to verify.')
        exists=raw!=b'ABSENT'
        if exists:
            expected=value['files']
            if not isinstance(expected,dict):raise ValueError('Partial operator manifest unavailable.')
            try:
                with tarfile.open(fileobj=io.BytesIO(raw),mode='r:*')as tar:
                    root_seen=False;seen=set()
                    for member in tar:
                        entry=member.name.removeprefix('./').rstrip('/')
                        if member.name in {'.','./'}:
                            if not member.isdir()or member.mode!=0o700 or member.uid!=0:
                                raise ValueError('Partial tool directory ownership/mode changed.')
                            root_seen=True;continue
                        if member.isdir()and entry=='migrations':continue
                        if (not member.isfile()or entry not in expected or entry in seen or member.size>50*1024*1024
                                or hashlib.sha256(tar.extractfile(member).read()).hexdigest()!=expected[entry]):
                            raise ValueError('Unknown or changed partial tool contents; refuse cleanup.')
                        seen.add(entry)
                    if not root_seen:raise ValueError('Partial tool directory identity unavailable.')
            except (tarfile.TarError,OSError):raise ValueError('Partial tool directory is unverifiable.')from None
            self.command(['exec','--user','0',self.catalog.container,'rm','-rf','--',value['directory']])
        if source:self.command(['rm',value['source']])
        value['stage']='removed';state.atomic(self.path,value)
        self.path.rename(archived);state.sync_directory(self.journal.directory)
        # Ordinary admission remains held. The caller may now prepare fresh tools
        # or accept the prior runtime, never blindly repeat an attempted SQL step.

    def cleanup(self):
        # Called only after successful runtime settlement or a separately reviewed
        # recovery. An exception never invokes cleanup or releases admission.
        self.check();value=self.record()
        if value['stage']!='ready':raise ValueError('Interrupted partial tool staging needs separate recovery; no blind deletion.')
        self.command(['exec','--user','0',self.catalog.container,'rm','-rf','--',value['directory']])
        value['stage']='removed';state.atomic(self.path,value)
