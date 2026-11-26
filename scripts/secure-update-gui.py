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
spec=importlib.util.spec_from_file_location('update_inventory',HERE/'secure-update-inventory.py')
inventory=importlib.util.module_from_spec(spec);spec.loader.exec_module(inventory)
FAILURE='Preparation could not be confirmed. Preserve any partial bundle for review. No update or service change was requested. Do not delete the bundle, open another window or choose a new folder to retry. Do not use legacy Upgrade as fallback.'

class Session:
    """One private-plan attempt per window, including an uncertain outcome."""
    def __init__(self,root,prepare=None,discover=None):
        self.root=pathlib.Path(root).resolve();self.prepare=prepare or backend.prepare
        self.attempted=False;self.lock=threading.Lock();self.discover=discover or inventory.inventory;self.snapshot=None
    def refresh(self):
        self.snapshot=None
        self.snapshot=self.discover(self.root)
        return self.snapshot
    def validate_choice(self,project,image):
        if self.snapshot is None:raise ValueError('Refresh local inventory and explicitly select a project and image first.')
        return inventory.choice(self.snapshot,project,image)
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
            path=self.validate(project,image,name);chosen=self.validate_choice(project,image)
            # Re-read metadata immediately before preparation. This does not replace
            # the backend's full Compose/image/container verification.
            if inventory.choice(self.discover(self.root),project,image)!=chosen:
                self.snapshot=None;raise ValueError('Local selection changed. Refresh and review again. No plan requested.')
            self.attempted=True
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
        frame=ttk.Frame(root,padding=16);frame.pack(fill='both',expand=True)
        ttk.Label(frame,text='Prepare a server update plan',font=('Sans',20,'bold')).pack(anchor='w')
        ttk.Label(frame,text='Preparation only. This window cannot install or run an update.',wraplength=760).pack(anchor='w',pady=(4,8))
        ttk.Label(frame,text='Use on this server only. Plans contain secrets: keep them here. No image download, schema install or service restart.',wraplength=760).pack(anchor='w',pady=(0,8))
        self.refresh_button=ttk.Button(frame,text='Refresh local projects and images',command=self.refresh_inventory);self.refresh_button.pack(anchor='w')
        self.inventory_status=tk.StringVar(value='No inventory read yet. Choose explicitly after refresh; no images pulled.')
        ttk.Label(frame,textvariable=self.inventory_status,wraplength=760).pack(anchor='w')
        self.fields=[];self.field_values=[]
        for label in ['Installed project (select, or type only for existing-plan review)','Local immutable image (platform match only, not provenance)','Private plan folder name (new request or existing review)']:
            ttk.Label(frame,text=label).pack(anchor='w',pady=(4,2));value=tk.StringVar();entry=ttk.Combobox(frame,textvariable=value,state='readonly')if len(self.fields)<2 else ttk.Entry(frame,textvariable=value);entry.pack(fill='x');self.fields.append(entry);self.field_values.append(value)
        ttk.Label(frame,text='New plans never overwrite files. Existing-plan review reads project/folder only, not the live runtime. It cannot repair or resume an update.',wraplength=760).pack(anchor='w',pady=6)
        self.consent=tk.BooleanVar(value=False)
        self.check=ttk.Checkbutton(frame,text='Create private diagnostic files only, not an update.',variable=self.consent);self.check.pack(anchor='w')
        controls=ttk.Frame(frame);controls.pack(fill='x',pady=8)
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
        self.fields[0].configure(state='normal');self.field_values[0].trace_add('write',self.project_changed)
        for value in self.field_values:value.trace_add('write',self.invalidate_review)
        root.protocol('WM_DELETE_WINDOW',self.close);self.poll_id=root.after(100,self.poll)
    def project_changed(self,*args):
        self.field_values[1].set('')
        value=self.session.snapshot
        if value:
            projects=[p for p in value['projects']if p['name']==self.fields[0].get()]
            self.fields[1].configure(values=[i['id']for i in value['images']if projects and i['platform']==projects[0]['platform']])
            self.inventory_status.set('Local image platform: '+projects[0]['platform']+'. Compatibility and provenance NOT checked.' if projects else 'Choose an inventoried project.')
    def refresh_inventory(self):
        if self.running:return
        self.field_values[0].set('');self.field_values[1].set('')
        self.fields[0].configure(values=[]);self.fields[1].configure(values=[])
        self.running=True;self.refresh_button.state(['disabled']);self.button.state(['disabled']);self.review_button.state(['disabled'])
        for entry in self.fields:entry.state(['disabled'])
        self.inventory_status.set('Reading local Docker metadata only. No pull, update or service operation.')
        def work():
            try:self.events.put(('inventory',self.session.refresh()))
            except Exception:self.events.put(('inventory',None))
        threading.Thread(target=work,daemon=False).start()
    def invalidate_review(self,*args):
        if self.running:return
        self.status.set('Inputs changed. Review again. No update requested.')
        self.output.configure(state='normal');self.output.delete('1.0','end');self.output.configure(state='disabled')
    def submit(self):
        if self.running or self.session.attempted:return
        args=tuple(e.get()for e in self.fields)
        try:self.session.validate(*args);chosen=self.session.validate_choice(*args[:2])
        except ValueError as error:self.status.set(str(error));return
        if not self.consent.get():self.status.set('Confirm private diagnostic file creation first. No plan requested.');return
        if not self.messagebox.askokcancel('Confirm private plan creation','Project: '+args[0]+'\nImage: '+args[1]+'\nPlatform: '+chosen['image']['platform']+' (compatibility/provenance NOT checked)\nPrivate folder: '+args[2]+'\n\nCreate this private diagnostic plan? No update will run. The bundle contains resolved secrets and must not be shared.',parent=self.root):
            self.status.set('Cancelled. No plan or update requested.');return
        self.running=True;self.refresh_button.state(['disabled']);self.button.state(['disabled']);self.review_button.state(['disabled']);self.check.state(['disabled'])
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
        self.running=True;self.refresh_button.state(['disabled']);self.button.state(['disabled']);self.review_button.state(['disabled'])
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
            if ok=='inventory':
                self.refresh_button.state(['!disabled']);self.review_button.state(['!disabled'])
                for entry in self.fields:entry.state(['!disabled'])
                self.fields[1].configure(state='readonly')
                if not self.session.attempted:self.button.state(['!disabled'])
                if text is None:self.inventory_status.set(inventory.ERROR)
                else:
                    self.fields[0].configure(values=[p['name']for p in text['projects']])
                    self.inventory_status.set(str(len(text['projects']))+' matching installed projects. Select one explicitly.' if text['projects'] else 'No eligible running project exactly bound to this installation. No automatic fallback. Existing-plan review remains available.')
                self.poll_id=self.root.after(100,self.poll);return
            self.refresh_button.state(['!disabled'])
            self.status.set('Snapshot of private files verified. Live runtime NOT checked; NOT ready to update.'if ok else 'Preparation outcome needs review. No retry from this window.')
            self.review_button.state(['!disabled'])
            for entry in self.fields:entry.state(['!disabled'])
            self.fields[1].configure(state='readonly')
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
