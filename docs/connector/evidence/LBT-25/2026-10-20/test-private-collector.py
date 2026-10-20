"""Offline P2 regressions: fake credentials and HTTP only; no SSH or real Cloud API."""
import contextlib,importlib.util,io,json,pathlib,subprocess,sys,tempfile,unittest
from unittest.mock import patch
import urllib.error,urllib.request
sys.dont_write_bytecode=True
ROOT=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('private_collector',ROOT/'run-private-collector.py')
collector=importlib.util.module_from_spec(spec);spec.loader.exec_module(collector)
PROJECT='63c7a8bdc0c647dcb85ba1c4a1899976'
AUTH={'username':'fixture-user','password':'fixture-password-only','project_id':PROJECT,
      'application_credential_secret':'EXCLUDED_AUTH_SENTINEL','token':'EXCLUDED_TOKEN_SENTINEL'}
LEDGER={'projectId':PROJECT,'resources':{'privateKey':'EXCLUDED_LEDGER_SENTINEL'}}


class Response(io.BytesIO):
    def __init__(self,data):
        super().__init__(json.dumps(data).encode())
        self.headers={'X-Subject-Token':'fixture-token-only'}


class CollectorTests(unittest.TestCase):
    def execute(self,private,project=PROJECT,http_error=False):
        requests=[];children=[]
        def urlopen(request,timeout):
            requests.append(request)
            if http_error:
                raise urllib.error.HTTPError(request.full_url,401,'fixture-password-only',None,None)
            if request.full_url.endswith('/identity/auth/tokens'):
                return Response({'token':{'project':{'id':project}}})
            server=request.full_url.rsplit('/',1)[1]
            instance={'27412c58-60bd-4b37-9bbd-78365be6681d':'ee63c707-3ef9-4492-ae74-5eab90474694',
                      '27725801-32c3-4c2b-b8f9-d2ea00e093a8':'458d4656-6983-4e93-bf59-42afa7ba1d4d'}[server]
            return Response({'server':{'id':server,'tenant_id':PROJECT,'status':'ACTIVE',
                'metadata':{'labbit_vm_key':'vm-1','labbit_workspace_vm_key':'vm-1',
                            'labbit_lab_instance_id':instance,'other':'EXCLUDED_RESPONSE_SENTINEL'}}})
        def child(args,**kwargs):
            children.append((args,kwargs))
            stdout=io.StringIO();code=0
            with patch.object(urllib.request,'urlopen',side_effect=urlopen),contextlib.redirect_stdout(stdout):
                try:exec(kwargs['input'].decode(),{})
                except SystemExit as error:code=error.code
            return subprocess.CompletedProcess(args,code,stdout.getvalue().encode(),b'')
        with patch.object(collector.subprocess,'run',side_effect=child):
            result=collector.collect(private)
        self.assertEqual(len(children),1)
        args,kwargs=children[0]
        self.assertEqual(args,[sys.executable,'-B','-'])
        self.assertEqual(set(kwargs),{'input','capture_output','timeout'})
        self.assertTrue(kwargs['capture_output']);self.assertEqual(kwargs['timeout'],90)
        self.assertNotIn('fixture-password-only',' '.join(args))
        for sentinel in ('EXCLUDED_AUTH_SENTINEL','EXCLUDED_TOKEN_SENTINEL','EXCLUDED_LEDGER_SENTINEL'):
            self.assertNotIn(sentinel,kwargs['input'].decode())
        self.assertNotIn('fixture-password-only',result.stdout.decode())
        self.assertNotIn('fixture-token-only',result.stdout.decode())
        self.assertNotIn('EXCLUDED_RESPONSE_SENTINEL',result.stdout.decode())
        return result,requests

    def test_only_required_fields_and_default_domain(self):
        private=collector.minimal_inputs(AUTH,LEDGER)
        self.assertEqual(private,{'auth':{'username':'fixture-user','password':'fixture-password-only',
            'user_domain_id':'default','project_id':PROJECT},'projectId':PROJECT})

    def test_local_auth_and_read_only_inventory_with_safe_metadata(self):
        private=collector.minimal_inputs(dict(AUTH,user_domain_id='fixture-domain'),LEDGER)
        result,requests=self.execute(private)
        self.assertEqual(result.returncode,0);self.assertEqual(len(requests),3)
        self.assertEqual([request.get_method() for request in requests],['POST','GET','GET'])
        self.assertTrue(all(request.full_url.startswith('https://api.ucloudbiz.olleh.com/d1/') for request in requests))
        body=json.loads(requests[0].data)
        self.assertEqual(body['auth']['identity']['password']['user']['domain'],{'id':'fixture-domain'})
        metadata=json.loads(result.stdout)
        self.assertTrue(metadata['readOnlyResourceAPI']);self.assertEqual(len(metadata['servers']),2)
        for row in metadata['servers']:
            self.assertEqual(set(row),{'instanceId','serverId','status','vmKeyIsVm1',
                'workspaceDesignationIsVm1','workspaceDesignationPresent','instanceAffinityMatches'})
            self.assertTrue(row['instanceAffinityMatches'])

    def test_wrong_project_prevents_resource_queries(self):
        result,requests=self.execute(collector.minimal_inputs(AUTH,LEDGER),project='different-project')
        self.assertEqual(result.returncode,1);self.assertEqual(len(requests),1)
        self.assertEqual(json.loads(result.stdout),{'collectorError':'AssertionError','httpStatus':None})

    def test_http_failure_suppresses_credential_error_text(self):
        result,requests=self.execute(collector.minimal_inputs(AUTH,LEDGER),http_error=True)
        self.assertEqual(result.returncode,1);self.assertEqual(len(requests),1)
        self.assertEqual(json.loads(result.stdout),{'collectorError':'HTTPError','httpStatus':401})

    def test_missing_required_field_fails_closed(self):
        for key in ('username','password','project_id'):
            auth=dict(AUTH);del auth[key]
            with self.assertRaises(KeyError):collector.minimal_inputs(auth,LEDGER)
        with self.assertRaises(KeyError):collector.minimal_inputs(AUTH,{})

    def test_protected_files_are_filtered_before_local_child(self):
        with tempfile.TemporaryDirectory(prefix='c3-fake-home-') as directory:
            home=pathlib.Path(directory)
            cloud=home/'.config/openstack/ktcloud-d1/clouds.yaml';cloud.parent.mkdir(parents=True)
            cloud.write_text(json.dumps({'clouds':{'ktcloud-d1':{'auth':AUTH}}}),encoding='utf-8')
            ledger=home/'.config/labbit/c3-connector-20261019/ktcloud-bootstrap-ledger.json'
            ledger.parent.mkdir(parents=True);ledger.write_text(json.dumps(LEDGER),encoding='utf-8')
            with patch.object(pathlib.Path,'home',return_value=home):private=collector.read_private()
            result,_=self.execute(private);self.assertEqual(result.returncode,0)

    def test_unsupported_consumer_rejected_before_protected_read(self):
        with tempfile.TemporaryDirectory(prefix='c3-fake-output-') as directory:
            output=pathlib.Path(directory)/'result.json';stdout=io.StringIO()
            with patch.object(collector,'reserve_output',return_value=output),\
                 patch.object(collector,'read_private') as read,\
                 patch.object(sys,'argv',['run-private-collector.py','different-script.py']),\
                 contextlib.redirect_stdout(stdout):
                self.assertEqual(collector.main(),1);read.assert_not_called()
            self.assertEqual(json.loads(output.read_text()),{'collectorError':'ValueError'})

    def test_local_input_failure_does_not_persist_source_excerpt(self):
        with tempfile.TemporaryDirectory(prefix='c3-fake-output-') as directory:
            output=pathlib.Path(directory)/'result.json';stdout=io.StringIO()
            with patch.object(collector,'reserve_output',return_value=output),\
                 patch.object(collector,'read_private',side_effect=ValueError('fixture-password-only')),\
                 patch.object(sys,'argv',['run-private-collector.py']),contextlib.redirect_stdout(stdout):
                self.assertEqual(collector.main(),1)
            self.assertEqual(json.loads(output.read_text()),{'collectorError':'ValueError'})
            self.assertNotIn('fixture-password-only',stdout.getvalue()+output.read_text())


if __name__=='__main__':unittest.main(verbosity=2)
