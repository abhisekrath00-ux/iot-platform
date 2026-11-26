#!/usr/bin/env python3
"""Fake-Docker failure cuts. No live daemon effects."""
import importlib.util,json,pathlib,subprocess,tempfile,types,unittest,os
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('cleanup',HERE/'real-application-fixture-cleanup.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Docker:
    def __init__(self):self.items={k:{}for k in ('container','image','network','volume')};self.calls=[];self.timeout=False;self.sticky=False
    def __call__(self,args,**kw):
        self.calls.append((args,kw));assert 0<kw['timeout']<=180
        if self.timeout:raise subprocess.TimeoutExpired(args,kw['timeout'])
        a=args[1:];kind=a[0];verb=a[1]
        if kind=='ps':kind='container';verb='ls'
        if verb=='ls':
            found=[]
            for name,item in self.items[kind].items():
                good=True
                for i,v in enumerate(a):
                    if v!='--filter':continue
                    f=a[i+1]
                    if f.startswith('label='):k,value=f[6:].split('=',1);good &=m.Owner.labels(None,item,kind).get(k)==value
                    elif f.startswith('name='):good &=name==f[5:].strip('^$/')
                    elif f.startswith('reference='):good &=name==f[10:]
                    elif f.startswith('volume='):good &=any(x.get('Name')==f[7:]for x in item.get('Mounts',[]))
                    else:raise AssertionError(f)
                if good:found.append(name)
            out='\n'.join(found)
        elif kind=='pull':
            self.items['image'][verb]={'Config':{'Volumes':{'/data':{}}}};out=''
        elif verb=='inspect':out=json.dumps([self.items[kind][a[2]]])
        elif verb=='rm':
            name=a[-1]
            if not self.sticky:self.items[kind].pop(name,None)
            out=''
        else:raise AssertionError(args)
        return types.SimpleNamespace(returncode=0,stdout=out)
class Tests(unittest.TestCase):
    def setup_owner(self,td):
        d=Docker();o=m.Owner.create(pathlib.Path(td)/'private.json',d);o.register_project('parent');o.register_project('parent-provider');o.register_images(['parent:old','parent:new','parent:operator']);return o,d
    def resource(self,o,d,kind,name,project=None):
        labels={m.LABEL:o.data['token']}
        if project:labels['com.docker.compose.project']=project
        d.items[kind][name]={'Config':{'Labels':labels},'Labels':labels,'Mounts':[]}
    def test_failure_cuts_and_idempotent_second_cleanup(self):
        for cut in ('before-build','partial-build','parent-start','provider-start','operator-interrupted','prepare-failure'):
            with self.subTest(cut=cut),tempfile.TemporaryDirectory()as td:
                o,d=self.setup_owner(td)
                if cut!='before-build':self.resource(o,d,'image','parent:new')
                if cut not in ('before-build','partial-build'):
                    for p in ('parent','parent-provider')if cut not in ('parent-start',)else('parent',):
                        base={'services':{'postgres':{'image':'postgres-base'}}};o.decorate(base,p)
                        self.resource(o,d,'volume',p+'-postgres-data-0');self.resource(o,d,'network',p+'_default')
                        self.resource(o,d,'container',p+'-db',p);d.items['container'][p+'-db']['Mounts']=[{'Type':'volume','Name':p+'-postgres-data-0'}]
                if cut=='operator-interrupted':self.resource(o,d,'container',o.operator('parent-provider'))
                o.cleanup();o.cleanup();self.assertTrue(all(not v for k,v in d.items.items()if k!='image'));self.assertEqual(set(d.items['image']),{'postgres-base'}if cut not in ('before-build','partial-build')else set());self.assertEqual(o.data['status'],'absent')
                self.assertEqual(o.path.stat().st_mode&0o777,0o600)
    def test_volume_created_before_container_is_owned_and_removed(self):
        with tempfile.TemporaryDirectory()as td:
            o,d=self.setup_owner(td)
            for project in ('parent','parent-provider'):
                base={'services':{'postgres':{'image':'postgres-base'}}};o.decorate(base,project)
                for name in [v['name']for v in base['volumes'].values()]:self.resource(o,d,'volume',name)
            o.cleanup();self.assertFalse(d.items['volume']);self.assertIn('postgres-base',d.items['image'])
    def test_diagnostic_failure_never_prevents_cleanup(self):
        with tempfile.TemporaryDirectory()as td:
            o,d=self.setup_owner(td);self.resource(o,d,'image','parent:new')
            def fail(*a,**k):raise subprocess.TimeoutExpired('logs',15)
            m.diagnostics(o,['docker','logs'],fail);o.cleanup();self.assertEqual(o.data['status'],'absent')
    def test_nonowned_refused_evidence_retained(self):
        with tempfile.TemporaryDirectory()as td:
            o,d=self.setup_owner(td);self.resource(o,d,'container','foreign','parent');d.items['container']['foreign']['Config']['Labels'][m.LABEL]='other'
            with self.assertRaises(ValueError):o.cleanup()
            self.assertIn('foreign',d.items['container']);self.assertEqual(json.loads(o.path.read_text())['status'],'unresolved');self.assertFalse(any(x[0][2]=='rm'for x in d.calls))
    def test_existing_resources_refused_before_registration(self):
        for kind,name in [('container','existing'),('image','parent:new'),('network','parent_default')]:
            with self.subTest(kind=kind),tempfile.TemporaryDirectory()as td:
                d=Docker();o=m.Owner.create(pathlib.Path(td)/'private.json',d);self.resource(o,d,kind,name,'parent')
                with self.assertRaises(ValueError):o.register_images(['parent:new'])if kind=='image'else o.register_project('parent')
    def test_timeout_or_sticky_absence_is_unresolved(self):
        for mode in ('timeout','sticky'):
            with tempfile.TemporaryDirectory()as td:
                o,d=self.setup_owner(td);self.resource(o,d,'image','parent:new');setattr(d,mode,True)
                with self.assertRaises(ValueError):o.cleanup()
                self.assertEqual(o.data['status'],'unresolved');self.assertTrue(o.path.exists())
    def test_manifest_atomic_private_collision_and_symlink_refusal(self):
        with tempfile.TemporaryDirectory()as td:
            o,d=self.setup_owner(td);before=o.path.read_bytes()
            with self.assertRaises(FileExistsError):m.Owner.create(o.path,d)
            self.assertEqual(before,o.path.read_bytes());link=pathlib.Path(td)/'link';link.symlink_to(o.path)
            with self.assertRaises(ValueError):m.Owner(link,d)
            self.assertFalse(list(pathlib.Path(td).glob('*.new')))
    def test_other_users_protect_owned_volume(self):
        with tempfile.TemporaryDirectory()as td:
            o,d=self.setup_owner(td);o.decorate({'services':{'postgres':{'image':'postgres-base'}}},'parent');v='parent-postgres-data-0'
            self.resource(o,d,'volume',v);self.resource(o,d,'container','ours','parent');self.resource(o,d,'container','foreign')
            for c in d.items['container'].values():c['Mounts']=[{'Type':'volume','Name':v}]
            with self.assertRaises(ValueError):o.cleanup()
            self.assertIn(v,d.items['volume']);self.assertIn('ours',d.items['container'])
    def test_separate_helper_instances_preserve_operator_records(self):
        with tempfile.TemporaryDirectory()as td:
            o,d=self.setup_owner(td);other=m.Owner(o.path,d)
            key=o.begin('up');other=m.Owner(o.path,d);name=other.operator('parent-provider');o.complete(key)
            self.assertIn(name,json.loads(o.path.read_text())['operators'])
    def test_interrupted_daemon_creation_never_claims_absence(self):
        for kind in ('build','parent-up','provider-up','operator-run'):
            with self.subTest(kind=kind),tempfile.TemporaryDirectory()as td:
                o,d=self.setup_owner(td);o.begin(kind)
                self.resource(o,d,'image','parent:new')
                with self.assertRaises(ValueError):o.cleanup()
                self.assertEqual(o.data['status'],'unresolved');self.assertFalse(d.items['image'])
                with self.assertRaises(ValueError):o.cleanup()
                self.assertTrue(o.data['pending'])
    def test_sources_cover_builds_and_always_same_ledger(self):
        s=(HERE/'test-secure-application-native.py').read_text();p=(HERE/'test-secure-application-provider-native.py').read_text();w=(HERE.parent/'.github/workflows/ci.yml').read_text().split('  real-application-preparation:\n')[1].split('  selected-application-generation:')[0]
        self.assertIn('try:return fixture_main()',s);self.assertIn('finally:owner=cleanup.Owner(path);owner.cleanup()',s);self.assertIn('owner.register_project(project);owner.register_images',s);self.assertIn('owner.register_project(project);owner.decorate(base,project)',p)
        self.assertIn('owner.operator(self.project)',s);self.assertIn('if: always()',w);self.assertEqual(w.count('hexthings-real-preparation-cleanup.json'),2);self.assertIn('timeout-minutes: 3',w)
if __name__=='__main__':unittest.main()
