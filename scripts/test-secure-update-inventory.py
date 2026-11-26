#!/usr/bin/env python3
"""Read-only inventory contracts using synthetic Docker responses only."""
import copy,importlib.util,json,os,pathlib,tempfile,types,unittest
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('inventory',HERE/'secure-update-inventory.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
OLD='sha256:'+'a'*64;NEW='sha256:'+'b'*64;OTHER='sha256:'+'c'*64
class Fixture:
    def __init__(self,root):
        self.root=root;self.calls=[];self.pinned=[];self.host='unix:///var/run/docker.sock';self.fail=False
        (root/'docker-compose.yml').write_text('fixture, no credentials')
        self.containers=[{'Id':str(n+1)*64,'Image':OLD,'Config':{'Env':['PASSWORD=private-secret'],'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.project.working_dir':str(root),'com.docker.compose.project.config_files':str(root/'docker-compose.yml'),'com.docker.compose.service':service}},'State':{'Status':'running'}}for n,service in enumerate(['api','ingest','mcp','web','mosquitto','postgres'])]
        self.images=[{'Id':ident,'Os':'linux','Architecture':arch,'Config':{'Env':['PASSWORD=private-secret']},'RepoTags':['private:tag']}for ident,arch in [(OLD,'amd64'),(NEW,'amd64'),(OTHER,'arm64')]]
    def execute(self,args,**kw):
        if args[:2]==['docker','--host']:
            self.pinned.append((args[2],dict(kw['env'])))
            args=['docker',*args[3:]]
        self.calls.append(args)
        if self.fail:raise OSError('private-secret')
        if args==['docker','context','inspect']:value=[{'Endpoints':{'docker':{'Host':self.host}}}]
        elif args[:4]==['docker','ps','--all','--quiet']:
            # Real CLI truncates quiet IDs unless --no-trunc was requested.
            return types.SimpleNamespace(returncode=0,stdout=' '.join(c['Id']if '--no-trunc'in args else c['Id'][:12]for c in self.containers))
        elif args[:2]==['docker','inspect']:value=self.containers
        elif args==['docker','image','ls','--no-trunc','--quiet']:return types.SimpleNamespace(returncode=0,stdout='\n'.join(i['Id']for i in self.images))
        elif args[:3]==['docker','image','inspect']:value=self.images
        else:raise AssertionError('Unexpected command '+repr(args))
        return types.SimpleNamespace(returncode=0,stdout=json.dumps(value))
class Tests(unittest.TestCase):
    def test_inventory_is_allowlisted_read_only_explicit_and_platform_bound(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);f=Fixture(root);before={p.name:p.read_bytes()for p in root.iterdir()}
            v=m.inventory(root,f.execute)
            self.assertEqual([p['name']for p in v['projects']],['fixture']);self.assertEqual(len(v['images']),3)
            self.assertNotIn('private-secret',json.dumps(v));self.assertNotIn('private:tag',json.dumps(v));self.assertNotIn(td,json.dumps(v))
            self.assertEqual(m.choice(v,'fixture',NEW)['image']['id'],NEW)
            for project,image in [('missing',NEW),('fixture',OTHER),('fixture','latest')]:
                with self.assertRaises(ValueError):m.choice(v,project,image)
            self.assertEqual(before,{p.name:p.read_bytes()for p in root.iterdir()})
    def test_missing_duplicate_cross_root_or_unhealthy_project_is_not_offered(self):
        for damage in ['empty','duplicate','crossroot','override','oneoff','stopped','unhealthy','missing-service']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);f=Fixture(root)
                if damage=='empty':f.containers=[]
                elif damage=='duplicate':
                    duplicate=copy.deepcopy(f.containers[0]);duplicate['Id']='f'*64;f.containers.append(duplicate)
                elif damage=='crossroot':f.containers[0]['Config']['Labels']['com.docker.compose.project.working_dir']='/other'
                elif damage=='override':f.containers[0]['Config']['Labels']['com.docker.compose.project.config_files']+=',/other'
                elif damage=='oneoff':f.containers[0]['Config']['Labels']['com.docker.compose.oneoff']='True'
                elif damage=='stopped':f.containers[0]['State']['Status']='exited'
                elif damage=='unhealthy':f.containers[0]['State']['Health']={'Status':'unhealthy'}
                elif damage=='missing-service':f.containers.pop()
                self.assertEqual(m.inventory(root,f.execute)['projects'],[])
    def test_remote_daemon_refuses_before_container_access(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);f=Fixture(root);f.host='ssh://private-secret'
            with self.assertRaises(ValueError):m.inventory(root,f.execute)
            self.assertEqual(f.calls,[['docker','context','inspect']])
    def test_local_access_failures_redact_upstream_detail(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);f=Fixture(root);f.fail=True
            with self.assertRaises(ValueError)as error:m.inventory(root,f.execute)
            self.assertNotIn('private-secret',str(error.exception))
    def test_multiple_projects_remain_explicit_no_first_match(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);f=Fixture(root);other=copy.deepcopy(f.containers)
            for n,c in enumerate(other):c['Config']['Labels']['com.docker.compose.project']='second';c['Id']=format(n+8,'x')*64
            f.containers+=other;v=m.inventory(root,f.execute)
            self.assertEqual([p['name']for p in v['projects']],['fixture','second'])
            with self.assertRaises(ValueError):m.choice(v,'',NEW)

    def test_committed_selected_configuration_binds_inventory_and_unsettled_state_refuses(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);f=Fixture(root);marker=root/'.secure-deployment';marker.mkdir(mode=0o700)
            config=root/'selected-runtime.json';m.state.atomic(config,{'services':{}});digest=m.state.digest(config);files={str(config):digest}
            m.state.atomic(marker/'selected.json',{'version':1,'project':'fixture','config':str(config),'sha256':digest,'files':files})
            m.state.atomic(marker/'journal.json',{'version':1,'phase':'committed','project':'fixture','candidate':str(config),'candidate_sha256':digest,'files':files})
            self.assertEqual(m.inventory(root,f.execute)['projects'],[])
            for c in f.containers:c['Config']['Labels']['com.docker.compose.project.config_files']=str(config)
            self.assertEqual(len(m.inventory(root,f.execute)['projects']),1)
            m.state.atomic(marker/'maintenance.json',{});f.calls=[]
            with self.assertRaises(ValueError):m.inventory(root,f.execute)
            self.assertEqual(f.calls,[['docker','context','inspect']])
    def test_invalid_image_metadata_or_inspect_partial_response_refuses(self):
        for damage in ['duplicate','platform','id','partial']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);f=Fixture(root)
                if damage=='duplicate':f.images.append(copy.deepcopy(f.images[0]))
                elif damage=='platform':f.images[0]['Architecture']='private-secret/path'
                elif damage=='id':f.images[0]['Id']='latest'
                else:
                    original=f.execute
                    def partial(args,**kw):
                        if 'inspect'in args and 'context'not in args:return types.SimpleNamespace(returncode=0,stdout=json.dumps(f.containers[:-1]))
                        return original(args,**kw)
                    f.execute=partial
                with self.assertRaises(ValueError):m.inventory(root,f.execute)

    def test_full_id_request_preserves_exact_inspect_identity_mapping(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);f=Fixture(root)
            short=f.execute(['docker','ps','--all','--quiet']).stdout.split()
            self.assertTrue(all(len(i)==12 for i in short))
            v=m.inventory(root,f.execute);self.assertEqual(len(v['projects']),1)
            self.assertIn(['docker','ps','--all','--quiet','--no-trunc','--filter','label=com.docker.compose.project'],f.calls)
            self.assertEqual({r['id']for r in v['projects'][0]['containers']},{c['Id']for c in f.containers})
            original=f.execute
            def wrong(args,**kw):
                result=original(args,**kw)
                if 'inspect'in args and 'context'not in args and 'image'not in args:
                    values=json.loads(result.stdout);values[0]['Id']='f'*64;return types.SimpleNamespace(returncode=0,stdout=json.dumps(values))
                return result
            with self.assertRaises(ValueError):m.inventory(root,wrong)
    def test_only_canonical_local_pipe_or_absolute_local_unix_endpoint(self):
        for host,allowed in [('npipe:////./pipe/docker_engine',True),('npipe:////./pipe/dockerDesktopLinuxEngine',True),('npipe:////server/pipe/docker_engine',False),('npipe:////./pipe/',False),('unix:///run/user/1000/docker.sock',True),('unix:////server/path',False),('tcp://127.0.0.1:2375',False)]:
            with self.subTest(host=host),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);f=Fixture(root);f.host=host
                if allowed:self.assertEqual(len(m.inventory(root,f.execute)['projects']),1)
                else:
                    with self.assertRaises(ValueError):m.inventory(root,f.execute)
                    self.assertEqual(f.calls,[['docker','context','inspect']])
    def test_absent_health_is_not_invented_healthy_and_present_status_must_pass(self):
        for health,allowed in [('absent',True),({'Status':'healthy'},True),({'Status':'starting'},False),({'Status':'unhealthy'},False),({},False),(None,False)]:
            with self.subTest(health=health),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);f=Fixture(root)
                if health!='absent':f.containers[0]['State']['Health']=health
                self.assertEqual(bool(m.inventory(root,f.execute)['projects']),allowed)

    def test_context_and_environment_drift_cannot_reroute_verified_local_reads(self):
        from unittest.mock import patch
        for endpoint in ['unix:///var/run/docker.sock','npipe:////./pipe/docker_engine']:
            with self.subTest(endpoint=endpoint),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);f=Fixture(root);f.host=endpoint
                original=f.execute
                def drift(args,**kw):
                    result=original(args,**kw)
                    if args==['docker','context','inspect']:
                        os.environ['DOCKER_CONTEXT']='remote-context'
                        os.environ['DOCKER_HOST']='ssh://remote.invalid'
                        f.host='tcp://remote.invalid:2375'
                    else:
                        self.assertEqual(args[:3],['docker','--host',endpoint])
                        for key in ['DOCKER_CONTEXT','DOCKER_HOST','DOCKER_TLS','DOCKER_TLS_VERIFY','DOCKER_CERT_PATH']:self.assertNotIn(key,kw['env'])
                    return result
                with patch.dict(os.environ,{'DOCKER_CONTEXT':'initial','DOCKER_HOST':'unix:///initial','DOCKER_TLS_VERIFY':'1','DOCKER_CERT_PATH':'private-path'}):
                    value=m.inventory(root,drift)
                self.assertEqual(len(value['projects']),1);self.assertTrue(f.pinned)
                self.assertTrue(all(host==endpoint for host,_ in f.pinned));self.assertNotIn(endpoint,json.dumps(value))
if __name__=='__main__':unittest.main()
