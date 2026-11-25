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
        tree=ast.parse((HERE/'test-secure-application-native.py').read_text());main=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='main')
        branch=next(n for n in ast.walk(main)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preparation_only'and not n.orelse)
        self.assertIsInstance(branch.body[-1],ast.Return)
        call=branch.body[0].value;self.assertEqual(call.func.attr,'prove');self.assertTrue(any(k.arg=='preparation_only'and k.value.value is True for k in call.keywords))
    def test_preparation_build_is_current_only_no_historical_fetch(self):
        tree=ast.parse((HERE/'test-secure-application-native.py').read_text());main=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='main')
        branch=next(n for n in ast.walk(main)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preparation_only'and n.orelse)
        text=ast.unparse(ast.Module(body=branch.body,type_ignores=[]))
        self.assertIn("sources = [('new', ROOT / 'server')]",text);self.assertIn('oldimage = newimage',text)
        self.assertNotIn('OLD_COMMIT',text);self.assertNotIn('fetch',text);self.assertNotIn('oldsrc',text)
    def test_provider_branch_does_not_enter_coordinator(self):
        tree=ast.parse((HERE/'test-secure-application-provider-native.py').read_text());f=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='prove')
        self.assertIs(f.args.defaults[-1].value,False)
        branch=next(n for n in ast.walk(f)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preparation_only')
        self.assertIsInstance(branch.body[-1],ast.Return)
        text=ast.unparse(branch);self.assertIn('gui.Session',text);self.assertIn('helper.probe',text);self.assertNotIn('coordinator(',text);self.assertNotIn('caller.run',text)
    def test_workflow_is_default_off_public_standard_no_storage(self):
        text=(HERE.parent/'.github/workflows/ci.yml').read_text();job=text.split('\n  real-application-preparation:\n')[1]
        self.assertIn('inputs.real_application_preparation',job);self.assertIn('github.event.repository.private == false',job);self.assertIn('runs-on: ubuntu-latest',job)
        self.assertIn('cache: false',job);self.assertNotIn('upload-artifact',job);self.assertIn('HEXTHINGS_REAL_PREPARATION_ONLY=1',job)
        block=text.split('      real_application_preparation:\n')[1].split('      native_update_preparation:')[0];self.assertIn('default: false',block)
if __name__=='__main__':unittest.main()
