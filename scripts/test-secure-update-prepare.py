#!/usr/bin/env python3
"""Operator entrypoint with fake Docker only, no owner installation effects."""
import copy,importlib.util,json,pathlib,subprocess,sys,tempfile,types,unittest,os,shutil
sys.dont_write_bytecode=True
HERE=pathlib.Path(__file__).resolve().parent
def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
m=load('secure-update-prepare');fixture=load('test-secure-application-docker');OLD='sha256:'+'a'*64;NEW='sha256:'+'b'*64
class Tests(unittest.TestCase):
    def test_preparation_from_inspection_only_preserves_all_non_writer_configuration(self):
        for secure in [False,True]:
            with self.subTest(secure=secure),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);bundle=root/'new-plan';config=fixture.Tests().config();calls=[]
                # Secure HTTPS sidecar is retained, not removed or changed by an
                # application image update. Selection itself is tested elsewhere.
                if secure:
                    config['services']['secure-web']={'image':OLD,'network_mode':'service:api','depends_on':['api','web'],'volumes':[]}
                    state=m.review.state;marker=root/'.secure-deployment';marker.mkdir(mode=0o700)
                    selected=root/'selected-runtime.json';state.atomic(selected,config)
                    digest=state.digest(selected);files={str(selected):digest}
                    state.atomic(marker/'selected.json',{'version':1,'project':'fixture','config':str(selected),'sha256':digest,'files':files})
                    state.atomic(marker/'journal.json',{'version':1,'phase':'committed','project':'fixture','candidate':str(selected),'candidate_sha256':digest,'files':files})
                compose=['docker','compose','-p','fixture']+(['-f',str(selected)]if secure else [])
                containers=[{'Id':'id-'+name,'Image':OLD,'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':name},'Env':[key+'='+value for key,value in s.get('environment',{}).items()],'Cmd':s.get('command'),'Entrypoint':None},'State':{'Status':'running'}}for name,s in config['services'].items()]
                def execute(args,**kw):
                    calls.append(args)
                    if args==compose+['config','--format','json']:value=config
                    elif args==compose+['ps','-a','-q']:return types.SimpleNamespace(returncode=0,stdout=' '.join(c['Id']for c in containers))
                    elif args==['docker','inspect',*[c['Id']for c in containers]]:value=containers
                    elif args[:3]==['docker','image','inspect']and args[3]in [OLD,NEW]:value=[{'Id':args[3],'Os':'linux','Architecture':'amd64','Config':{'Env':[]}}]
                    elif args[:5]==['docker','compose','-p','fixture','-f']and args[-2:]==['config','--quiet']:return types.SimpleNamespace(returncode=0,stdout='')
                    else:raise AssertionError('Not a permitted read-only command: '+str(args))
                    return types.SimpleNamespace(returncode=0,stdout=json.dumps(value))
                value=m.prepare(root,'fixture',NEW,bundle,execute)
                self.assertFalse(value['ready_to_update']);self.assertFalse(value['changes_allowed'])
                old=m.review.state.read_private(bundle/'previous-runtime.json');new=m.review.state.read_private(bundle/'candidate-runtime.json');normalized=copy.deepcopy(new)
                for name in ['api','ingest','mcp']:normalized['services'][name]['image']=OLD
                self.assertEqual(old,normalized)
                for p in bundle.iterdir():self.assertEqual(p.stat().st_mode&0o777,0o600)
                self.assertEqual(bundle.stat().st_mode&0o777,0o700)
                before={p.name:p.read_bytes()for p in bundle.iterdir()};n=len(calls)
                with self.assertRaises(ValueError):m.prepare(root,'fixture',NEW,bundle,execute)
                self.assertEqual(n,len(calls));self.assertEqual(before,{p.name:p.read_bytes()for p in bundle.iterdir()})
                text=m.review.display(value);self.assertNotIn('synthetic',text);self.assertNotIn(str(root),text)
                self.assertEqual((root/'.secure-deployment').exists(),secure)
                if secure:self.assertEqual(m.review.state.selection(root)['config'],str(selected))
    def test_invalid_or_interrupted_state_does_not_call_docker(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td)
            def denied(*a,**kw):raise AssertionError('unexpected Docker')
            for project,image in [('Fixture',NEW),('fixture','latest')]:
                with self.assertRaises(ValueError):m.prepare(root,project,image,root/'new',denied)
            marker=root/'.secure-deployment';marker.mkdir(mode=0o700);(marker/'maintenance.json').write_text('{}')
            with self.assertRaises(ValueError):m.prepare(root,'fixture',NEW,root/'new',denied)
            self.assertFalse((root/'new').exists())
    def test_failed_docker_is_called_once_without_partial_receipt(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);calls=[]
            def failed(args,**kw):
                calls.append(args);return types.SimpleNamespace(returncode=1,stdout='private-upstream-detail')
            with self.assertRaises(ValueError)as failure:m.prepare(root,'fixture',NEW,root/'new',failed)
            self.assertEqual(len(calls),1);self.assertNotIn('private-upstream-detail',str(failure.exception));self.assertEqual(list(root.iterdir()),[])
    def test_terminal_failure_redacts_private_docker_output_and_does_not_retry(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);scripts=root/'scripts';shutil.copytree(HERE,scripts,ignore=shutil.ignore_patterns('__pycache__'))
            bin=root/'bin';bin.mkdir();log=root/'calls';docker=bin/'docker'
            docker.write_text('#!/bin/sh\necho called >> "$CALL_LOG"\necho private-Docker-detail\necho private-Docker-error >&2\nexit 19\n');docker.chmod(0o700)
            env=dict(os.environ,PATH=str(bin)+':'+os.environ['PATH'],CALL_LOG=str(log))
            p=subprocess.run(['bash',str(scripts/'setup.sh'),'secure-update-prepare','fixture',NEW,str(root/'new')],capture_output=True,text=True,env=env)
            self.assertEqual(p.returncode,1);self.assertNotIn('private-Docker',p.stdout+p.stderr);self.assertEqual(log.read_text(),'called\n');self.assertFalse((root/'new').exists())
    def test_terminal_subcommand_missing_inputs_refuses_without_effects(self):
        p=subprocess.run(['bash',str(HERE/'setup.sh'),'secure-update-prepare'],capture_output=True,text=True)
        self.assertEqual(p.returncode,2);self.assertIn('No update requested',p.stdout)
if __name__=='__main__':unittest.main()
