#!/usr/bin/env python3
"""Pure GUI controller contracts. Actual Tk pixels checked separately."""
import importlib.util,pathlib,tempfile,unittest,concurrent.futures
spec=importlib.util.spec_from_file_location('gui',pathlib.Path(__file__).with_name('secure-update-gui.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
IMAGE='sha256:'+'b'*64
class Tests(unittest.TestCase):
    def test_invalid_inputs_never_prepare(self):
        with tempfile.TemporaryDirectory()as td:
            calls=[];session=m.Session(td,lambda*a:calls.append(a))
            for p,i,n in [('Other',IMAGE,'new'),('fixture','latest','new'),('fixture',IMAGE,'../escape'),('fixture',IMAGE,'/absolute')]:
                with self.assertRaises(ValueError):session.run(p,i,n)
            self.assertFalse(session.attempted);self.assertEqual(calls,[]);self.assertEqual(list(pathlib.Path(td).iterdir()),[])
    def test_existing_or_symlink_bundle_never_overwrites(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);(root/'old').mkdir();(root/'link').symlink_to(root/'missing')
            for name in ['old','link']:
                with self.assertRaises(ValueError):m.Session(root,lambda*a:self.fail()).run('fixture',IMAGE,name)
    def test_uncertain_backend_redacted_and_one_attempt_across_threads(self):
        with tempfile.TemporaryDirectory()as td:
            calls=[]
            def fail(*args):calls.append(args);raise ValueError('private-source-secret')
            session=m.Session(td,fail)
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
            session=m.Session(root,lambda*a:m.backend.review.inspect(root,'fixture',receipt))
            text=session.run('fixture',IMAGE,'new');self.assertIn('NOT approval',text);self.assertNotIn(str(root),text)
            with self.assertRaises(ValueError):session.run('fixture',IMAGE,'another')
if __name__=='__main__':unittest.main()
