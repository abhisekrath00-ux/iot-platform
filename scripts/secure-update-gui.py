#!/usr/bin/env python3
"""Local desktop update preparation, no listener or deployment activation.

Tk is imported only by main. No credentials, Docker controls or browser bridge
are exposed to other processes over a network. A plan contains resolved secrets.
"""
import importlib.util
import pathlib
import queue
import re
import sys
import threading
sys.dont_write_bytecode=True
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('update_prepare',HERE/'secure-update-prepare.py')
backend=importlib.util.module_from_spec(spec);spec.loader.exec_module(backend)
FAILURE='Preparation could not be confirmed. Preserve any partial bundle for review. No update or service change was requested. Do not delete the bundle, open another window or choose a new folder to retry. Do not use legacy Upgrade as fallback.'

class Session:
    """One private-plan attempt per window, including an uncertain outcome."""
    def __init__(self,root,prepare=None):
        self.root=pathlib.Path(root).resolve();self.prepare=prepare or backend.prepare
        self.attempted=False;self.lock=threading.Lock()
    def validate(self,project,image,name):
        if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',project):raise ValueError('Enter the exact existing Compose project name.')
        if not re.fullmatch(r'sha256:[a-f0-9]{64}',image):raise ValueError('Select the exact already-local image ID, starting sha256: and 64 lowercase hex characters.')
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]{0,63}',name):raise ValueError('Use a new plan folder name with letters, numbers, underscores or hyphens.')
        path=self.root/name
        if path.exists()or path.is_symlink():raise ValueError('That plan folder already exists. Preserve it; choose a genuinely new plan only after reviewing the earlier attempt.')
        return path
    def run(self,project,image,name):
        with self.lock:
            if self.attempted:raise ValueError('This window already requested a plan. No retry is allowed here.')
            path=self.validate(project,image,name);self.attempted=True
        try:return backend.review.display(self.prepare(self.root,project,image,path))
        except Exception:raise ValueError(FAILURE)from None

    def validate_existing(self,project,name):
        if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',project):
            raise ValueError('Enter the exact existing Compose project name.')
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]{0,63}',name):
            raise ValueError('Enter the existing plan folder name inside this installation.')
        bundle=self.root/name
        if bundle.is_symlink() or not bundle.is_dir() or bundle.stat().st_mode & 0o077:
            raise ValueError('Existing plan must be a private real folder inside this installation. Nothing changed.')
        return bundle
    def review_existing(self,project,name):
        # Read only. A prior/partial attempt is never a new prepare operation.
        bundle=self.validate_existing(project,name)
        with self.lock:self.attempted=True
        try:
            value=backend.review.inspect(self.root,project,bundle/'preflight.json')
            return backend.review.display(value)
        except (OSError,ValueError,KeyError,TypeError):
            raise ValueError('Existing plan is incomplete, changed or cannot be verified. Nothing changed. Preserve this folder and do not create a replacement or use legacy Upgrade. Ask for a local recovery review before another preparation attempt. Do not share plan files: they may contain secrets.') from None

class Window:
    def __init__(self,root,tk,ttk,messagebox,session):
        self.root=root;self.session=session;self.messagebox=messagebox;self.events=queue.Queue();self.running=False
        root.title('HexThings | Prepare server update');root.geometry('860x760');root.minsize(650,600)
        frame=ttk.Frame(root,padding=24);frame.pack(fill='both',expand=True)
        ttk.Label(frame,text='Prepare a server update plan',font=('Sans',20,'bold')).pack(anchor='w')
        ttk.Label(frame,text='Preparation only. This window cannot install or run an update.',wraplength=760).pack(anchor='w',pady=(8,16))
        ttk.Label(frame,text='Use this window on the server itself. Keep the private plan here: it contains resolved secrets. No images are downloaded, no schema is installed and no services are restarted.',wraplength=760).pack(anchor='w',pady=(0,16))
        self.fields=[];self.field_values=[]
        for label in ['Existing Compose project name','Already-local candidate image ID (sha256:...)','Private plan folder name (new request or existing review)']:
            ttk.Label(frame,text=label).pack(anchor='w',pady=(8,4));value=tk.StringVar();entry=ttk.Entry(frame,textvariable=value);entry.pack(fill='x');self.fields.append(entry);self.field_values.append(value)
        ttk.Label(frame,text='New plans go inside this installation and never overwrite existing files. Existing-plan review uses project and folder only. It reads files, not the live installation, and cannot resume an update.',wraplength=760).pack(anchor='w',pady=12)
        self.consent=tk.BooleanVar(value=False)
        self.check=ttk.Checkbutton(frame,text='Create private diagnostic files only, not an update.',variable=self.consent);self.check.pack(anchor='w')
        controls=ttk.Frame(frame);controls.pack(fill='x',pady=14)
        self.button=ttk.Button(controls,text='Review plan request',command=self.submit);self.button.pack(side='left')
        self.review_button=ttk.Button(controls,text='Review existing plan',command=self.review_existing);self.review_button.pack(side='left',padx=8)
        ttk.Button(controls,text='Close',command=self.close).pack(side='right')
        self.status=tk.StringVar(value='No plan requested. Current installation unchanged.')
        ttk.Label(frame,textvariable=self.status,wraplength=760).pack(anchor='w',pady=(0,10))
        output_frame=ttk.Frame(frame);output_frame.pack(fill='both',expand=True)
        self.output=tk.Text(output_frame,width=1,wrap='word',height=12,state='disabled',font=('Sans',11))
        scroll=ttk.Scrollbar(output_frame,orient='vertical',command=self.output.yview);scroll.pack(side='right',fill='y')
        self.output.configure(yscrollcommand=scroll.set);self.output.pack(side='left',fill='both',expand=True)
        def resize(event):
            for widget in frame.winfo_children():
                if isinstance(widget,ttk.Label):widget.configure(wraplength=max(250,event.width-48))
        frame.bind('<Configure>',resize)
        for value in self.field_values:value.trace_add('write',self.invalidate_review)
        root.protocol('WM_DELETE_WINDOW',self.close);self.poll_id=root.after(100,self.poll)
    def invalidate_review(self,*args):
        if self.running:return
        self.status.set('Inputs changed. No current review for these fields. Review again; no update requested.')
        self.output.configure(state='normal');self.output.delete('1.0','end');self.output.configure(state='disabled')
    def submit(self):
        if self.running or self.session.attempted:return
        args=tuple(e.get()for e in self.fields)
        try:self.session.validate(*args)
        except ValueError as error:self.status.set(str(error));return
        if not self.consent.get():self.status.set('Confirm private diagnostic file creation first. No plan requested.');return
        if not self.messagebox.askokcancel('Confirm private plan creation','Project: '+args[0]+'\nImage: '+args[1]+'\nPrivate folder: '+args[2]+'\n\nCreate this private diagnostic plan? No update will run. The bundle contains resolved secrets and must not be shared.',parent=self.root):
            self.status.set('Cancelled. No plan or update requested.');return
        self.running=True;self.button.state(['disabled']);self.review_button.state(['disabled']);self.check.state(['disabled'])
        for entry in self.fields:entry.state(['disabled'])
        self.status.set('Checking the exact local installation and preparing private files. No automatic retry. Do not close this window.')
        def work():
            try:self.events.put((True,self.session.run(*args)))
            except Exception:self.events.put((False,FAILURE))
        threading.Thread(target=work,daemon=False).start()
    def review_existing(self):
        if self.running:return
        project,_,name=tuple(e.get()for e in self.fields)
        try:self.session.validate_existing(project,name)
        except (ValueError,OSError) as error:
            self.status.set('Enter an existing private plan folder and valid project. Nothing changed.');return
        self.running=True;self.button.state(['disabled']);self.review_button.state(['disabled'])
        self.check.state(['disabled'])
        for entry in self.fields:entry.state(['disabled'])
        self.status.set('Reading existing private plan files only. No Docker, update or retry requested.')
        def work():
            try:self.events.put((True,self.session.review_existing(project,name)))
            except ValueError as error:self.events.put((False,str(error)))
            except Exception:self.events.put((False,FAILURE))
        threading.Thread(target=work,daemon=False).start()
    def poll(self):
        try:
            ok,text=self.events.get_nowait();self.running=False
            self.status.set('Snapshot of private files verified. Live runtime NOT checked; NOT ready to update.'if ok else 'Preparation outcome needs review. No retry from this window.')
            self.review_button.state(['!disabled'])
            for entry in self.fields:entry.state(['!disabled'])
            self.output.configure(state='normal');self.output.delete('1.0','end');self.output.insert('1.0',text);self.output.configure(state='disabled')
        except queue.Empty:pass
        self.poll_id=self.root.after(100,self.poll)
    def close(self):
        if self.running:
            self.messagebox.showwarning('Preparation in progress','Wait for the current check to finish. Closing is not cancellation of a file operation.',parent=self.root);return
        if self.poll_id is not None:
            self.root.after_cancel(self.poll_id);self.poll_id=None
        self.root.destroy()

def main():
    try:
        import tkinter as tk
        from tkinter import ttk,messagebox
        root=tk.Tk()
    except Exception:
        print('A local graphical desktop and Python Tk are required. No plan or update requested.',file=sys.stderr);return 1
    Window(root,tk,ttk,messagebox,Session(HERE.parent));root.mainloop();return 0
if __name__=='__main__':sys.exit(main())
