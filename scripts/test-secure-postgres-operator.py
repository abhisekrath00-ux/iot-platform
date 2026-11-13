#!/usr/bin/env python3
"""Local schema channel contracts, no owner Docker or database effects."""
import hashlib
import importlib.util
import io
import json
import pathlib
import subprocess
import tarfile
import tempfile
import unittest

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('localop',HERE/'secure-postgres-operator.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class Catalog:
    container='a'*64;user='ci';database='application'
    def sql(self,statement):return {'socket':'/var/run/postgresql','port':'5432'}if "'port'"in statement else{'sockets':'/var/run/postgresql'}

def archive(files):
    result=io.BytesIO()
    with tarfile.open(fileobj=result,mode='w')as tar:
        for name,raw in files.items():
            info=tarfile.TarInfo(name);info.size=len(raw);tar.addfile(info,io.BytesIO(raw))
    return result.getvalue()

class Tests(unittest.TestCase):
    def fixture(self,root,journal):
        m.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'a'*64})
        calls=[];binary=archive({'setupctl':b'\x7fELFfixture'});migrations=archive({'migrations/0001_base.sql':b'select 1;'})
        def run(args,**kwargs):
            calls.append((args,kwargs))
            if args[1]=='inspect':
                value={'Name':'/'+args[-1],'Image':'sha256:'+'b'*64,'State':{'Status':'created'},'HostConfig':{'NetworkMode':'none'},'Config':{'Labels':{'hexthings.schema-tool':args[-1]}}}
                output=json.dumps([value])
            elif args[1]=='cp'and args[-1]=='-':output=binary if '/bin/setupctl'in args[-2]else migrations
            elif 'sha256sum'in args:
                record=operator.record();names=[name for name in sorted(record['files'])if record['directory']+'/'+name in args];output='\n'.join(record['files'][name]+'  '+record['directory']+'/'+name for name in names)+'\n'
            elif args[-1]=='connection-readiness':output=json.dumps({'version':1,'status':'connected','database':'application','user':'ci'})
            elif args[-1]in {'inspect-reviewed','apply-reviewed'}:output='{"status":"exact_reviewed_additive_93"}'
            else:output=''if kwargs['text']else b''
            return subprocess.CompletedProcess(args,0,output,''if kwargs['text']else b'')
        operator=m.LocalOperator(journal,Catalog(),'sha256:'+'b'*64,lambda:None,run)
        return operator,calls
    def test_static_archive_manifest_no_untrusted_paths_or_types(self):
        packed,manifest=m.LocalOperator.archive(archive({'setupctl':b'\x7fELFtest'}),archive({'migrations/0001_base.sql':b'select 1;'}))
        self.assertEqual(manifest['setupctl'],hashlib.sha256(b'\x7fELFtest').hexdigest())
        with tarfile.open(fileobj=io.BytesIO(packed))as tar:self.assertEqual([x.mode for x in tar],[0o400,0o500])
        for bad in ['../outside','migrations/../outside','migrations/link','/migrations/0001_base.sql']:
            with self.subTest(bad=bad),self.assertRaises(ValueError):
                m.LocalOperator.archive(archive({'setupctl':b'\x7fELFtest'}),archive({bad:b'anything'}))
        with self.assertRaises(ValueError):m.LocalOperator.archive(archive({'setupctl':b'not ELF'}),archive({'migrations/0001_base.sql':b'x'}))
    def test_prepared_exact_image_tools_run_only_inside_pg_with_local_socket(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            operator,calls=self.fixture(pathlib.Path(td),journal);operator.prepare()
            self.assertEqual(operator.record()['stage'],'ready')
            backup=pathlib.Path(td)/'backup.dump';backup.write_bytes(b'verified-fixture');backup.chmod(0o600)
            value={'schema_attempted':m.application.ADDED,'backup':{'path':str(backup),'sha256':m.state.digest(backup),'verified_archive':True}}
            for action in ['inspect-reviewed','apply-reviewed']:self.assertEqual(operator(value,action)['status'],'exact_reviewed_additive_93')
            runs=[(args,kwargs)for args,kwargs in calls if args[-1]in {'inspect-reviewed','apply-reviewed'}]
            self.assertEqual(len(runs),2)
            for args,kwargs in runs:
                self.assertEqual(args[1:3],['exec','-i']);self.assertIn('a'*64,args)
                self.assertTrue(any('host=%2Fvar%2Frun%2Fpostgresql'in value for value in args))
                self.assertNotIn('run',args);self.assertNotIn('shell',kwargs)
            self.assertIsNone(runs[0][1]['input']);self.assertIn('"writers_stopped": true',runs[1][1]['input'])
            operator.cleanup();self.assertEqual(operator.record()['stage'],'removed')
            with self.assertRaises(ValueError):operator({},'inspect-reviewed')
    def test_saved_state_scope_bad_socket_or_changed_tool_refuse_no_network_fallback(self):
        for problem in ['scope','socket','tool','guard','partial']:
            with self.subTest(problem=problem),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                root=pathlib.Path(td);operator,calls=self.fixture(root,journal);operator.prepare()
                if problem=='scope':m.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'c'*64})
                if problem=='socket':operator.catalog.sql=lambda _: {'sockets':'/var/run/postgresql,/tmp'}
                if problem=='guard':operator.guard=lambda:True
                if problem=='partial':
                    value=operator.record();value['stage']='copying';m.state.atomic(operator.path,value)
                if problem=='tool':
                    original=operator.run
                    def changed(args,**kw):
                        if 'sha256sum'in args:return subprocess.CompletedProcess(args,0,'wrong','')
                        return original(args,**kw)
                    operator.run=changed
                before=len(calls)
                with self.assertRaises(ValueError):operator({},'inspect-reviewed')
                self.assertFalse(any(args[-1]in {'inspect-reviewed','apply-reviewed'}for args,_ in calls[before:]))
    def test_explicit_partial_staging_recovery_checks_artifacts_and_never_replays_sql(self):
        for problem in ['none','unknown','changed','source-running']:
            with self.subTest(problem=problem),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                root=pathlib.Path(td);tool,calls=self.fixture(root,journal);tool.prepare()
                value=tool.record();value['stage']='copying';m.state.atomic(tool.path,value)
                original=tool.run
                output=io.BytesIO()
                with tarfile.open(fileobj=output,mode='w')as tar:
                    member=tarfile.TarInfo('.');member.type=tarfile.DIRTYPE;member.mode=0o700;tar.addfile(member)
                    content=b'changed'if problem=='changed'else b'\x7fELFfixture'
                    member=tarfile.TarInfo('./unknown'if problem=='unknown'else './setupctl');member.size=len(content);member.mode=0o500;tar.addfile(member,io.BytesIO(content))
                def staged(args,**kw):
                    if args[1:3]==['container','ls']:return subprocess.CompletedProcess(args,0,'source-id','')
                    if 'hexthings-tool-check'in args:return subprocess.CompletedProcess(args,0,output.getvalue(),b'')
                    result=original(args,**kw)
                    if problem=='source-running'and args[1]=='inspect':
                        rows=json.loads(result.stdout);rows[0]['State']['Status']='running';result.stdout=json.dumps(rows)
                    return result
                tool.run=staged;before=len(calls)
                if problem=='none':
                    tool.recover_staging();self.assertFalse(tool.path.exists())
                    self.assertEqual(len(list(journal.directory.glob('*-discarded.json'))),1)
                    with self.assertRaises(ValueError):m.state.selection(root)
                else:
                    with self.assertRaises(ValueError):tool.recover_staging()
                    self.assertEqual(tool.record()['stage'],'copying')
                self.assertFalse(any(args[-1]in {'apply-reviewed','inspect-reviewed'}for args,_ in calls[before:]))

    def test_exact_local_connection_stdin_empty_env_and_narrow_results(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            tool,calls=self.fixture(pathlib.Path(td),journal);tool.prepare()
            self.assertEqual(tool.connection(),{'version':1,'status':'connected','database':'application','user':'ci'})
            args,kw=calls[-1];self.assertEqual(args[-3:],['-i',tool.record()['directory']+'/setupctl','connection-readiness'])
            request=json.loads(kw['input']);self.assertEqual(request['transport'],'local_socket');self.assertEqual(request['password'],'')
            self.assertFalse(any(x.startswith('DATABASE_URL=')for x in args))
            original=tool.run
            for outcome in [{'version':1,'status':'database_closed'},{'version':1,'status':'authentication_failed'},
                            {'version':1,'status':'unverified'},{'version':1,'status':'hba_denied'}]:
                def reply(args,**kw):
                    if args[-1]=='connection-readiness':return subprocess.CompletedProcess(args,0,json.dumps(outcome),'')
                    return original(args,**kw)
                tool.run=reply;self.assertEqual(tool.connection(),outcome)
    def test_local_connection_changed_tool_or_socket_or_outcome_refuses(self):
        for damage in ['tool','socket','port','extra','server','user','version','unknown','oversize','partial']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                tool,calls=self.fixture(pathlib.Path(td),journal);tool.prepare();ordinary=tool.run
                if damage=='socket':tool.catalog.sql=lambda _:dict(socket='/tmp,/run',port='5432')
                if damage=='port':tool.catalog.sql=lambda _:dict(socket='/tmp',port='0')
                if damage=='partial':
                    v=tool.record();v['stage']='copying';m.state.atomic(tool.path,v)
                def changed(args,**kw):
                    if damage=='tool'and 'sha256sum'in args:return subprocess.CompletedProcess(args,0,'different','')
                    if args[-1]=='connection-readiness':
                        value={'version':1,'status':'connected','database':'application','user':'ci'}
                        if damage=='extra':value['other']=True
                        if damage=='server':value['server']='172.20.0.2'
                        if damage=='user':value['user']='other'
                        if damage=='version':value['version']=True
                        if damage=='unknown':value={'version':1,'status':'not_supported'}
                        return subprocess.CompletedProcess(args,0,'x'*5000 if damage=='oversize'else json.dumps(value),'')
                    return ordinary(args,**kw)
                tool.run=changed
                with self.assertRaises(ValueError):tool.connection()

    def test_adapter_routes_explicit_operator_only_after_writer_verification(self):
        spec=importlib.util.spec_from_file_location('app',HERE/'secure-application-docker.py');app=importlib.util.module_from_spec(spec);spec.loader.exec_module(app)
        obj=object.__new__(app.DockerApplication);events=[]
        obj.verify_writers_stopped=lambda _:events.append('stopped')
        obj.maintenance_operator=lambda value,action:events.append(action)or {'version':1,'status':'exact_reviewed_additive_93','runtime_accepted':False}
        obj.operator({},'inspect-reviewed')
        self.assertEqual(events,['stopped','inspect-reviewed'])
        def refuse(_):raise ValueError('writer still running')
        obj.verify_writers_stopped=refuse
        with self.assertRaises(ValueError):obj.operator({},'apply-reviewed')
        self.assertNotIn('apply-reviewed',events)

if __name__=='__main__':unittest.main()
