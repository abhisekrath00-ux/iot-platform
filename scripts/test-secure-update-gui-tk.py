#!/usr/bin/env python3
"""Actual Tk event-loop contracts. No Docker or installation changes.

Run under a local display (or Xvfb). Default discovery skips without Tk/display.
Required local acceptance (fails without either dependency):
    xvfb-run -a /usr/bin/python3 scripts/test-secure-update-gui-tk.py --require-display

Exact h300 local command, using privately extracted Ubuntu Tk libraries:
    PYTHONPATH=/downloads/h300/tk-local/usr/lib/python3.10:/downloads/h300/tk-local/usr/lib/python3.10/lib-dynload LD_LIBRARY_PATH=/downloads/h300/tk-local/usr/lib xvfb-run -a /usr/bin/python3 scripts/test-secure-update-gui-tk.py --require-display

This suite is explicit local acceptance, not provisioned or required by hosted CI.
"""
import importlib.util,pathlib,threading,time,tempfile,unittest,sys
REQUIRE_DISPLAY='--require-display'in sys.argv
if REQUIRE_DISPLAY:sys.argv.remove('--require-display')
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('gui',HERE/'secure-update-gui.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
try:
    import tkinter as tk
    from tkinter import ttk
except ImportError:
    tk=None
class Boxes:
    def __init__(self):self.confirmations=0;self.warnings=0;self.confirm=True
    def askokcancel(self,*a,**kw):self.confirmations+=1;return self.confirm
    def showwarning(self,*a,**kw):self.warnings+=1
class Tests(unittest.TestCase):
    def setUp(self):
        if tk is None:
            if REQUIRE_DISPLAY:self.fail('Required display acceptance failed: Python Tk is unavailable')
            self.skipTest('Python Tk is unavailable')
        try:self.root=tk.Tk()
        except tk.TclError:
            if REQUIRE_DISPLAY:self.fail('Required display acceptance failed: a local graphical display is unavailable')
            self.skipTest('A local graphical display is unavailable')
        self.temp=tempfile.TemporaryDirectory();self.boxes=Boxes()
    def tearDown(self):
        if hasattr(self,'root'):
            try:
                if hasattr(self,'w') and self.root.winfo_exists():self.w.close()
                else:self.root.destroy()
            except tk.TclError:pass
        if hasattr(self,'temp'):self.temp.cleanup()
    def window(self,prepare=None):
        self.snapshot={'projects':[{'name':'fixture','platform':'linux/amd64','containers':[]}],'images':[{'id':'sha256:'+'b'*64,'platform':'linux/amd64'}]}
        self.session=m.Session(self.temp.name,prepare,lambda*a:self.snapshot);self.session.refresh();self.w=m.Window(self.root,tk,ttk,self.boxes,self.session)
        for variable,value in zip(self.w.field_values,['fixture','sha256:'+'b'*64,'new']):variable.set(value)
        self.w.consent.set(True);self.root.update()
    def settle(self):
        end=time.monotonic()+3
        while self.w.running and time.monotonic()<end:self.root.update();time.sleep(.01)
        self.assertFalse(self.w.running)
    def test_close_and_repeated_submit_during_operation_never_cancel_or_retry(self):
        started,release=threading.Event(),threading.Event();calls=[]
        def prepare(*a):
            calls.append(a);started.set()
            if not release.wait(3):raise AssertionError('Fixture worker timed out')
            raise ValueError('private-worker-secret')
        self.window(prepare)
        try:
            self.w.submit();self.assertTrue(started.wait(1));self.w.close();self.w.submit();self.w.review_existing();self.root.update()
            self.assertTrue(self.root.winfo_exists());self.assertEqual(self.boxes.warnings,1);self.assertEqual(len(calls),1)
            self.assertTrue(all('disabled'in e.state()for e in self.w.fields))
        finally:release.set()
        self.settle();self.assertNotIn('private-worker-secret',self.w.output.get('1.0','end'))
        self.assertIn('disabled',self.w.button.state());self.w.submit();self.assertEqual(len(calls),1)
        self.w.close()
    def test_idle_close_cancels_scheduled_poll(self):
        self.window(lambda*a:self.fail())
        pending=self.w.poll_id;self.assertIn(pending,self.root.tk.call('after','info'))
        self.w.close();self.assertIsNone(self.w.poll_id);self.assertNotIn(pending,self.root.tk.call('after','info'))
    def test_cancelled_confirmation_does_not_latch_or_create_plan(self):
        self.window(lambda*a:self.fail('Cancelled request must not prepare'));self.boxes.confirm=False;self.w.submit()
        self.assertFalse(self.session.attempted);self.assertFalse(self.w.running)
        self.assertEqual(list(pathlib.Path(self.temp.name).iterdir()),[])
    def test_reopened_partial_plan_read_only_recovery_without_consent_or_image(self):
        self.window(lambda*a:self.fail('Recovery must not prepare'));plan=pathlib.Path(self.temp.name)/'partial';plan.mkdir(mode=0o700)
        (plan/'candidate-runtime.json').write_text('private-partial-secret');self.w.field_values[1].set('');self.w.fields[2].delete(0,'end');self.w.fields[2].insert(0,'partial');self.w.consent.set(False)
        self.w.review_existing();self.settle();self.assertEqual(self.boxes.confirmations,0)
        self.assertIn('incomplete',self.w.output.get('1.0','end'));self.assertNotIn('private-partial-secret',self.w.output.get('1.0','end'))
        self.assertEqual((plan/'candidate-runtime.json').read_text(),'private-partial-secret');self.assertIn('disabled',self.w.button.state())
    def test_editing_after_review_clears_old_result_and_does_not_enable_prepare(self):
        self.window(lambda*a:self.fail());plan=pathlib.Path(self.temp.name)/'partial';plan.mkdir(mode=0o700)
        self.w.fields[2].delete(0,'end');self.w.fields[2].insert(0,'partial');self.w.review_existing();self.settle()
        self.assertIn('incomplete',self.w.output.get('1.0','end'));self.w.fields[2].delete(0,'end');self.w.fields[2].insert(0,'another')
        self.assertEqual(self.w.output.get('1.0','end').strip(),'');self.assertIn('Inputs changed',self.w.status.get());self.assertIn('disabled',self.w.button.state())
    def test_invalid_existing_folder_does_not_disable_correction(self):
        self.window(lambda*a:self.fail());self.w.review_existing()
        self.assertFalse(self.w.running);self.assertFalse(self.session.attempted);self.assertNotIn('disabled',self.w.fields[0].state())

    def test_inventory_refresh_clears_selections_and_never_autochooses(self):
        self.window(lambda*a:self.fail());self.w.refresh_inventory();self.settle()
        self.assertEqual(self.w.fields[0].get(),'');self.assertEqual(self.w.fields[1].get(),'')
        self.assertEqual(tuple(self.w.fields[0]['values']),('fixture',));self.assertFalse(self.session.attempted)
        self.w.field_values[0].set('fixture');self.w.project_changed()
        self.assertEqual(tuple(self.w.fields[1]['values']),('sha256:'+'b'*64,));self.assertEqual(self.w.fields[1].get(),'')
    def test_failed_inventory_keeps_existing_plan_review_available(self):
        self.window(lambda*a:self.fail())
        def failed(*a):raise ValueError('private-secret')
        self.session.discover=failed;self.w.refresh_inventory();self.settle()
        self.assertIsNone(self.session.snapshot);self.assertNotIn('private-secret',self.w.inventory_status.get())
        self.assertNotIn('disabled',self.w.review_button.state());self.assertEqual(self.w.fields[0].get(),'')
    def test_small_supported_window_keeps_result_and_actions_visible(self):
        self.window(lambda*a:self.fail());self.root.geometry('650x600');self.root.update()
        for widget in [self.w.button,self.w.review_button,self.w.refresh_button,self.w.output]:
            self.assertTrue(widget.winfo_ismapped());self.assertLess(widget.winfo_rooty(),600)
        self.assertGreater(self.w.output.winfo_height(),60)
if __name__=='__main__':unittest.main()
