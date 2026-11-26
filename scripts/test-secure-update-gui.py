#!/usr/bin/env python3
"""Pure GUI controller contracts. Actual Tk pixels checked separately."""
import importlib.util,pathlib,tempfile,unittest,concurrent.futures
spec=importlib.util.spec_from_file_location('gui',pathlib.Path(__file__).with_name('secure-update-gui.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
IMAGE='sha256:'+'b'*64
SNAPSHOT={'projects':[{'name':'fixture','platform':'linux/amd64','containers':[]}],'images':[{'id':IMAGE,'platform':'linux/amd64'}]}
def make_session(root,prepare):
    value=m.Session(root,prepare,lambda*a:SNAPSHOT);value.refresh();return value
class Tests(unittest.TestCase):
    def test_invalid_inputs_never_prepare(self):
        with tempfile.TemporaryDirectory()as td:
            calls=[];session=make_session(td,lambda*a:calls.append(a))
            for p,i,n in [('Other',IMAGE,'new'),('fixture','latest','new'),('fixture',IMAGE,'../escape'),('fixture',IMAGE,'/absolute')]:
                with self.assertRaises(ValueError):session.run(p,i,n)
            self.assertFalse(session.attempted);self.assertEqual(calls,[]);self.assertEqual(list(pathlib.Path(td).iterdir()),[])
    def test_existing_or_symlink_bundle_never_overwrites(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);(root/'old').mkdir();(root/'link').symlink_to(root/'missing')
            for name in ['old','link']:
                with self.assertRaises(ValueError):make_session(root,lambda*a:self.fail()).run('fixture',IMAGE,name)
    def test_uncertain_backend_redacted_and_one_attempt_across_threads(self):
        with tempfile.TemporaryDirectory()as td:
            calls=[]
            def fail(*args):calls.append(args);raise ValueError('private-source-secret')
            session=make_session(td,fail)
            def attempt(_):
                try:session.run('fixture',IMAGE,'new')
                except ValueError as error:return str(error)
            with concurrent.futures.ThreadPoolExecutor(4)as pool:results=list(pool.map(attempt,range(4)))
            self.assertEqual(len(calls),1);self.assertTrue(session.attempted);self.assertNotIn('private-source-secret',' '.join(results))
    def test_success_is_redacted_review_not_update_authority(self):
        with tempfile.TemporaryDirectory()as td:
            # Real planner/review through an existing tested simulated Docker fixture.
            spec=importlib.util.spec_from_file_location('fixture',pathlib.Path(__file__).with_name('test-secure-application-docker.py'));f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
            root=pathlib.Path(td);receipt,_,_=f.Tests().plan(root)
            session=make_session(root,lambda*a:m.backend.review.inspect(root,'fixture',receipt))
            text=session.run('fixture',IMAGE,'new');self.assertIn('NOT approval',text);self.assertNotIn(str(root),text)
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'another')

    def test_restarted_session_reads_existing_plan_without_prepare_or_writes(self):
        with tempfile.TemporaryDirectory()as td:
            spec=importlib.util.spec_from_file_location('fixture',pathlib.Path(__file__).with_name('test-secure-application-docker.py'));f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
            root=pathlib.Path(td);f.Tests().plan(root)
            before={str(p):p.read_bytes()for p in root.rglob('*')if p.is_file()}
            session=make_session(root,lambda*a:self.fail('Review must not prepare'))
            for _ in range(2):
                text=session.review_existing('fixture','plan')
                self.assertIn('NOT approval',text);self.assertIn('NOT verified',text);self.assertNotIn('synthetic',text);self.assertNotIn(td,text)
            self.assertEqual(before,{str(p):p.read_bytes()for p in root.rglob('*')if p.is_file()})
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'replacement')
    def test_partial_restart_preserves_private_diagnostics_and_latches_no_retry(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);plan=root/'partial';plan.mkdir(mode=0o700);evidence=plan/'candidate-runtime.json';evidence.write_text('secret-partial-detail');evidence.chmod(0o600)
            session=make_session(root,lambda*a:self.fail('Partial plan must not prepare'))
            with self.assertRaises(ValueError)as failure:session.review_existing('fixture','partial')
            self.assertNotIn('secret-partial-detail',str(failure.exception));self.assertIn('incomplete',str(failure.exception));self.assertEqual(evidence.read_text(),'secret-partial-detail')
            self.assertEqual(list(plan.iterdir()),[evidence]);self.assertTrue(session.attempted)
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'replacement')
    def test_existing_review_rejects_path_escape_symlink_and_nonprivate_folder(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);(root/'plan').mkdir(mode=0o700);(root/'link').symlink_to(root/'plan');(root/'public').mkdir(mode=0o755)
            for project,name in [('Fixture','plan'),('fixture','../plan'),('fixture','/plan'),('fixture','link'),('fixture','missing'),('fixture','public')]:
                session=make_session(root,lambda*a:self.fail())
                with self.assertRaises(ValueError):session.review_existing(project,name)
                self.assertFalse(session.attempted)

    def test_explicit_review_refresh_detects_changed_plan_without_leaking_bytes(self):
        with tempfile.TemporaryDirectory()as td:
            spec=importlib.util.spec_from_file_location('fixture',pathlib.Path(__file__).with_name('test-secure-application-docker.py'));f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
            root=pathlib.Path(td);receipt,_,candidate=f.Tests().plan(root)
            session=make_session(root,lambda*a:self.fail());session.review_existing('fixture','plan')
            candidate.write_text('secret-modified-plan')
            with self.assertRaises(ValueError)as failure:session.review_existing('fixture','plan')
            self.assertNotIn('secret-modified-plan',str(failure.exception));self.assertEqual(candidate.read_text(),'secret-modified-plan')

    def test_missing_inventory_or_stale_recheck_refuses_before_prepare(self):
        import copy
        with tempfile.TemporaryDirectory()as td:
            session=m.Session(td,lambda*a:self.fail('must not prepare'),lambda*a:SNAPSHOT)
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'new')
            session.refresh();changed=copy.deepcopy(SNAPSHOT);changed['projects'][0]['containers']=[{'id':'changed'}]
            session.discover=lambda*a:changed
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'new')
            self.assertFalse(session.attempted);self.assertIsNone(session.snapshot);self.assertEqual(list(pathlib.Path(td).iterdir()),[])
    def test_failed_refresh_clears_previously_usable_snapshot(self):
        with tempfile.TemporaryDirectory()as td:
            session=make_session(td,lambda*a:self.fail())
            def fail(*a):raise ValueError('fixture failure')
            session.discover=fail
            with self.assertRaises(ValueError):session.refresh()
            self.assertIsNone(session.snapshot)

    def test_default_backend_all_subordinate_calls_keep_preview_transport_after_drift(self):
        import json,os,types
        from unittest.mock import patch
        spec=importlib.util.spec_from_file_location('prepfixture',pathlib.Path(__file__).with_name('test-secure-update-prepare.py'));f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
        for endpoint in ['unix:///var/run/docker.sock','npipe:////./pipe/docker_engine']:
            with self.subTest(endpoint=endpoint),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);(root/'docker-compose.yml').write_text('fixture config')
                config=f.fixture.Tests().config();calls=[];contexts=[]
                containers=[{'Id':format(n+1,'x')*64,'Image':f.OLD,'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':name,'com.docker.compose.project.working_dir':str(root),'com.docker.compose.project.config_files':str(root/'docker-compose.yml')},'Env':[key+'='+value for key,value in service.get('environment',{}).items()],'Cmd':service.get('command'),'Entrypoint':None},'State':{'Status':'running'}}for n,(name,service)in enumerate(config['services'].items())]
                def execute(args,**kw):
                    calls.append(args)
                    if args==['docker','context','inspect']:
                        contexts.append(args);return types.SimpleNamespace(returncode=0,stdout=json.dumps([{'Endpoints':{'docker':{'Host':endpoint}}}]))
                    self.assertEqual(args[:3],['docker','--host',endpoint])
                    for key in ['DOCKER_CONTEXT','DOCKER_HOST','DOCKER_TLS','DOCKER_TLS_VERIFY','DOCKER_CERT_PATH']:self.assertNotIn(key,kw['env'])
                    args=['docker',*args[3:]]
                    if args[:2]==['docker','ps']or args[-3:]==['ps','-a','-q']:return types.SimpleNamespace(returncode=0,stdout=' '.join(c['Id']for c in containers))
                    if args==['docker','image','ls','--no-trunc','--quiet']:return types.SimpleNamespace(returncode=0,stdout=f.OLD+'\n'+IMAGE)
                    if args[:2]==['docker','inspect']:value=containers
                    elif args[:3]==['docker','image','inspect']:value=[{'Id':i,'Os':'linux','Architecture':'amd64','Config':{'Env':[]}}for i in args[3:]]
                    elif args[-3:]==['config','--format','json']:value=config
                    elif args[-2:]==['config','--quiet']:return types.SimpleNamespace(returncode=0,stdout='')
                    else:raise AssertionError('Unexpected subordinate command '+repr(args))
                    return types.SimpleNamespace(returncode=0,stdout=json.dumps(value))
                with patch.object(m.inventory.subprocess,'run',side_effect=execute),patch.dict(os.environ,{'DOCKER_CONTEXT':'initial'}):
                    session=m.Session(root);session.refresh();session.validate_choice('fixture',IMAGE)
                    os.environ['DOCKER_CONTEXT']='remote';os.environ['DOCKER_HOST']='ssh://remote.invalid'
                    text=session.run('fixture',IMAGE,'plan')
                self.assertIn('NOT approval',text);self.assertEqual(len(contexts),1)
                self.assertTrue((root/'plan/preflight.json').is_file())
                self.assertEqual(sum(args[-2:]==['config','--quiet']for args in calls),2)
                self.assertTrue(any(args[-3:]==['config','--format','json']for args in calls))
                self.assertNotIn(endpoint,text);self.assertNotIn(endpoint,(root/'plan/preflight.json').read_text())
                print('BOUND_TRANSPORT_PROOF='+json.dumps({'transport':'local-pipe'if endpoint.startswith('npipe:')else'local-unix','context_resolutions':len(contexts),'pinned_calls':len(calls)-len(contexts),'backend_compose_config_checks':sum(args[-2:]==['config','--quiet']for args in calls),'commands':[args[3:]for args in calls if args[:2]==['docker','--host']]},sort_keys=True))
    def test_default_backend_cannot_use_unbound_custom_discovery(self):
        with tempfile.TemporaryDirectory()as td:
            session=m.Session(td,discover=lambda*a:SNAPSHOT)
            with self.assertRaises(ValueError):session.refresh()
            self.assertIsNone(session.snapshot);self.assertIsNone(session._transport)
            session.snapshot=SNAPSHOT
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'plan')
            self.assertFalse(session.attempted);self.assertEqual(list(pathlib.Path(td).iterdir()),[])
    def test_failed_bound_refresh_drops_transport_and_snapshot(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory()as td:
            session=m.Session(td)
            with patch.object(m.inventory,'LocalTransport')as factory,patch.object(m.inventory,'scan',return_value=SNAPSHOT):
                session.refresh();self.assertIsNotNone(session._transport)
                factory.side_effect=ValueError('fixture failure')
                with self.assertRaises(ValueError):session.refresh()
            self.assertIsNone(session._transport);self.assertIsNone(session.snapshot)
if __name__=='__main__':unittest.main()
