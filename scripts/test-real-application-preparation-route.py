#!/usr/bin/env python3
"""Route/guard contracts only. Actual Docker workload is a separate opt-in job."""
import ast,os,pathlib,subprocess,sys,unittest
HERE=pathlib.Path(__file__).resolve().parent
class Tests(unittest.TestCase):
    def test_nonrunner_refuses_before_any_docker(self):
        env=dict(os.environ);env.pop('GITHUB_ACTIONS',None);env['HEXTHINGS_REAL_PREPARATION_ONLY']='1'
        r=subprocess.run([sys.executable,str(HERE/'test-secure-application-native.py')],env=env,capture_output=True,text=True,timeout=10)
        self.assertNotEqual(r.returncode,0);self.assertIn('Disposable standard Ubuntu runner only',r.stderr)
    def test_preparation_branch_returns_before_mutation_proofs(self):
        tree=ast.parse((HERE/'test-secure-application-native.py').read_text());main=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='fixture_main')
        branch=next(n for n in ast.walk(main)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preparation_only'and isinstance(n.body[-1],ast.Return))
        self.assertIsInstance(branch.body[-1],ast.Return)
        call=branch.body[0].value;self.assertEqual(call.func.attr,'prove');self.assertTrue(any(k.arg=='preparation_only'and k.value.value is True for k in call.keywords))
    def test_preparation_build_is_current_only_no_historical_fetch(self):
        tree=ast.parse((HERE/'test-secure-application-native.py').read_text());main=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='fixture_main')
        branch=next(n for n in ast.walk(main)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preparation_only'and n.orelse)
        text=ast.unparse(ast.Module(body=branch.body,type_ignores=[]))
        self.assertIn("sources = [('new', ROOT / 'server')]",text);self.assertNotIn('oldimage = newimage',text)
        self.assertNotIn('OLD_COMMIT',text);self.assertNotIn('fetch',text);self.assertNotIn('oldsrc',text)
    def test_variant_preserves_layers_and_requires_distinct_ids(self):
        text=(HERE/'test-secure-application-native.py').read_text()
        self.assertIn("LABEL hexthings.fixture.preparation=running-current",text)
        self.assertIn("metadata[0]['Id']==metadata[1]['Id']",text)
        self.assertIn("metadata[0]['RootFS']!=metadata[1]['RootFS']",text)
    def test_real_planner_refuses_same_image_before_plan_files(self):
        import importlib.util,tempfile,json,types
        spec=importlib.util.spec_from_file_location('testprep',HERE/'test-secure-update-prepare.py');f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);config=f.fixture.Tests().config()
            containers=[{'Id':'id-'+n,'Image':f.OLD,'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':n},'Env':[k+'='+v for k,v in s.get('environment',{}).items()],'Cmd':s.get('command'),'Entrypoint':None},'State':{'Status':'running'}}for n,s in config['services'].items()]
            def execute(args,**kw):
                if args[-3:]==['config','--format','json']:value=config
                elif args[-3:]==['ps','-a','-q']:return types.SimpleNamespace(returncode=0,stdout=' '.join(c['Id']for c in containers))
                elif args[:2]==['docker','inspect']:value=containers
                elif args[:3]==['docker','image','inspect']:value=[{'Id':f.OLD,'Os':'linux','Architecture':'amd64','Config':{'Env':[]}}]
                else:raise AssertionError(args)
                return types.SimpleNamespace(returncode=0,stdout=json.dumps(value))
            with self.assertRaisesRegex(ValueError,'different local API image'):f.m.prepare(root,'fixture',f.OLD,root/'plan',execute)
            self.assertFalse((root/'plan').exists())
    def test_provider_branch_does_not_enter_coordinator(self):
        tree=ast.parse((HERE/'test-secure-application-provider-native.py').read_text());f=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='prove')
        self.assertIs(f.args.defaults[-1].value,False)
        branch=next(n for n in ast.walk(f)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preparation_only'and isinstance(n.body[-1],ast.Return))
        self.assertIsInstance(branch.body[-1],ast.Return)
        text=ast.unparse(branch);self.assertIn('gui.Session',text);self.assertIn('helper.probe',text);self.assertNotIn('coordinator(',text);self.assertNotIn('caller.run',text)
    def test_preparation_jwt_fixture_matches_both_clients_and_server(self):
        import importlib.util
        spec=importlib.util.spec_from_file_location('native',HERE/'test-secure-application-native.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
        value='synthetic-preparation-only-not-for-deployment-2026'
        self.assertGreaterEqual(len(value),32);self.assertGreaterEqual(len(set(value)),8)
        text=(HERE/'test-secure-application-native.py').read_text();provider=(HERE/'test-secure-application-provider-native.py').read_text()
        self.assertIn("'JWT_SIGNING_SECRET':FIXTURE_JWT",text);self.assertIn('hmac.new(FIXTURE_JWT.encode()',text);self.assertIn('hmac.new(fixture.FIXTURE_JWT.encode()',provider)
        self.assertIn(value,text);self.assertIn(value,provider)
    def test_workflow_is_default_off_public_standard_no_storage(self):
        text=(HERE.parent/'.github/workflows/ci.yml').read_text();job=text.split('\n  real-application-preparation:\n')[1]
        self.assertIn('inputs.real_application_preparation',job);self.assertIn('github.event.repository.private == false',job);self.assertIn('runs-on: ubuntu-latest',job)
        self.assertIn('cache: false',job);self.assertNotIn('upload-artifact',job);self.assertIn('HEXTHINGS_REAL_PREPARATION_ONLY=1',job)
        block=text.split('      real_application_preparation:\n')[1].split('      native_update_preparation:')[0];self.assertIn('default: false',block)

    def test_native_gui_callers_refresh_inventory_and_keep_actual_compose_path(self):
        text=(HERE/'test-secure-application-provider-native.py').read_text()
        self.assertIn('selected=path;m.state.atomic(selected,resolved)',text)
        self.assertIn('session=gui.Session(root);session.refresh();text=session.run',text)
        synthetic=(HERE/'test-secure-update-preparation-native.py').read_text()
        self.assertIn('session=gui.Session(root);session.refresh();text=session.run',synthetic)
        self.assertIn('missing=gui.Session(root);missing.refresh()',synthetic)
        self.assertIn("'-f',str(root/'selected-runtime.json')",synthetic)
        self.assertNotIn("selected=root/'selected-runtime.json'",text)
if __name__=='__main__':unittest.main()
