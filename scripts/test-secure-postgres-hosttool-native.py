#!/usr/bin/env python3
"""Disposable actual image -> never-started source -> private host ELF proof.
No namespace or provider connection acceptance claimed by this staging test.
"""
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('hosttool',HERE/'secure-postgres-hosttool.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Loss(BaseException):pass

def prove(image,parent_root,project):
    if (os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux'or os.geteuid()!=0
            or parent_root.parent!=pathlib.Path('/tmp')or not parent_root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')):raise ValueError('Disposable native host tool fixture only.')
    pending=['create','rm'];names=[]
    def runner(args,**kwargs):
        if args[1]=='create':names.append(args[args.index('--name')+1])
        result=subprocess.run(args,**kwargs)
        if pending and args[1]==pending[0]:pending.pop(0);raise Loss('Synthetic actual staging lost acknowledgement')
        return result
    with tempfile.TemporaryDirectory(prefix='hexthings-hosttool-ci-')as td:
        root=pathlib.Path(td)
        try:
            with m.state.Journal(root)as journal:
                m.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'scope':'application_bootstrap','project':project})
                def guard():
                    value=json.loads(subprocess.run(['docker','image','inspect',image],capture_output=True,text=True,check=True,timeout=10).stdout)
                    if len(value)!=1 or value[0]['Id']!=image:raise ValueError('Exact candidate image unavailable.')
                try:m.HostTool(journal,image,guard,runner).prepare()
                except Loss:pass
                else:raise ValueError('Real source-create lost ack was not injected.')
            with m.state.Journal(root)as journal:
                try:m.HostTool(journal,image,guard,runner).resume()
                except Loss:pass
                else:raise ValueError('Real source-remove lost ack was not injected.')
            with m.state.Journal(root)as journal:
                tool=m.HostTool(journal,image,guard,runner);binary=tool.resume()
                if tool.ready()!=binary or pending:raise ValueError('Fresh host staging recovery unverified.')
                result=subprocess.run([str(binary),'connection-readiness'],input='{}',capture_output=True,text=True,timeout=5)
                if result.returncode==0 or result.stdout:raise ValueError('Invalid explicit host probe request did not refuse.')
                # The exact staged candidate static executable ran on the host,
                # but no endpoint or network namespace was supplied or approved.
                tool.release()
                if binary.exists()or tool.record()['stage']!='released':raise ValueError('Host binary cleanup unverified.')
                print('PASS actual static host probe staging: exact never-started network-none candidate image source, private hash-checked amd64 ELF, real create/remove lost-ack fresh recoveries, host execution refuses missing explicit endpoint, checked binary/source cleanup. No namespace/provider connection/owner authority proof.',flush=True)
        finally:
            # Disposable fixture hygiene only. Production staging never erases
            # unrelated/changed evidence after a failed identity/hash check.
            for name in names:subprocess.run(['docker','rm','-f',name],capture_output=True,text=True,timeout=10)
