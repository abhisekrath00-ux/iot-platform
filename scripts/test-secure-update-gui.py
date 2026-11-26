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
if __name__=='__main__':unittest.main()
