import base64
import copy
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('store_module', Path(__file__).with_name('dr_control_artifact_store.py'))
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)


class StoreTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.source = self.root/'approval.json'; self.receipt=self.root/'receipt.json'
        self.now=dt.datetime(2026,11,23,tzinfo=dt.timezone.utc)
        self.raw=json.dumps({'Version':'labbit-dr-control/v1','KeyID':'sample','Signature':base64.b64encode(b'x'*64).decode(),'Payload':{'Version':'labbit-dr-control/v1','Action':'init','RequestSHA256':'a'*64,'Principal':'sample','ID':'00000000-0000-4000-8000-000000000001','NotBefore':'2026-11-20T00:00:00Z','ExpiresAt':'2026-11-20T00:01:00Z'}}).encode()
        self.source.write_bytes(self.raw)
        self.p={'version':'dr-control-artifact-store/v1','account':'123456789012','actor':'arn:aws:iam::123456789012:user/fixture','profile':'fixture','aws':str(self.root/'aws.exe'),'region':'ap-northeast-3','bucket':'fixture-bucket','prefix':'control/trial','key':'control/trial/approval.json','sha256':hashlib.sha256(self.raw).hexdigest(),'size':len(self.raw),'not_before':'2026-11-22T23:59:00Z','valid_until':'2026-11-23T00:01:00Z','retain_until':'2026-12-24T00:00:00Z','owner':'fixture','enable_upload':True}
        self.calls=[]; self.failure=''

    def aws_fake(self,p,s,a,args):
        self.calls.append((s,a,args))
        if a=='get-caller-identity':return {'Account':p['account'],'Arn':p['actor']}
        if a=='get-bucket-versioning':return {'Status':'Enabled'}
        if a=='put-object':
            # Public source snapshot and durable intent exist BEFORE request.
            self.assertEqual(m.strict(self.receipt.read_bytes())['status'],'INTENT')
            self.assertEqual(Path(args[args.index('--body')+1]).read_bytes(),self.raw)
            self.assertEqual(args[args.index('--if-none-match')+1],'*')
            self.assertIn('--expected-bucket-owner',args)
            if self.failure=='put':raise m.Hold('response lost')
            return {'VersionId':'fixed-version','ServerSideEncryption':'AES256'}
        if a=='head-object':
            self.assertEqual(args[args.index('--version-id')+1],'fixed-version')
            return {'VersionId':'fixed-version','ContentLength':len(self.raw)+(1 if self.failure=='larger-object' else 0),'ServerSideEncryption':'AES256'}
        if a=='get-object':
            self.assertEqual(args[args.index('--version-id')+1],'fixed-version')
            if self.failure=='get':raise m.Hold('read denied')
            Path(args[-1]).write_bytes(self.raw if self.failure!='corrupt' else self.raw+b'x')
            return {'VersionId':'fixed-version','ContentLength':len(self.raw),'ServerSideEncryption':'AES256'}
        raise AssertionError(a)

    def execute(self,execute=True):
        return m.store(self.p,self.source,self.receipt,execute,self.aws_fake,lambda:self.now)

    def test_preview_no_identity_no_s3_no_journal(self):
        self.assertEqual(self.execute(False)['status'],'PREVIEW_ONLY')
        self.assertEqual(self.calls,[]);self.assertFalse(self.receipt.exists())

    def test_exact_original_version_and_repeat_read_only(self):
        r=self.execute();self.assertEqual(r['status'],'READBACK_VERIFIED')
        self.assertEqual(r['reference']['SHA256'],self.p['sha256'])
        self.execute()
        self.assertEqual(sum(a=='put-object' for _,a,_ in self.calls),1)
        self.assertEqual(sum(a=='get-object' for _,a,_ in self.calls),2)
        self.assertFalse(self.receipt.with_name('receipt.json.lock').exists())

    def test_uncertain_put_keeps_unknown_and_never_retries(self):
        self.failure='put'
        with self.assertRaises(m.Hold):self.execute()
        self.assertEqual(m.strict(self.receipt.read_bytes())['status'],'UNKNOWN')
        self.failure=''
        with self.assertRaises(m.Hold):self.execute()
        self.assertEqual(sum(a=='put-object' for _,a,_ in self.calls),1)

    def test_read_failure_preserves_version_and_can_only_read_again(self):
        for failure in ('get','corrupt'):
            with self.subTest(failure=failure):
                self.receipt.unlink(missing_ok=True);self.calls=[];self.failure=failure
                with self.assertRaises(m.Hold):self.execute()
                self.assertEqual(m.strict(self.receipt.read_bytes())['status'],'STORED')
                self.failure='';self.execute()
                self.assertEqual(sum(a=='put-object' for _,a,_ in self.calls),1)

    def test_existing_intent_or_lock_blocks_put(self):
        self.receipt.write_text('{"status":"INTENT"}')
        with self.assertRaises(m.Hold):self.execute()
        self.assertFalse(any(a=='put-object' for _,a,_ in self.calls))
        lock=self.receipt.with_name('receipt.json.lock');lock.write_text('')
        self.calls=[]
        with self.assertRaises(m.Hold):self.execute()
        self.assertEqual(self.calls,[])

    def test_conflicting_plan_never_reuploads(self):
        self.execute();self.p['key']='control/trial/other.json'
        with self.assertRaises(m.Hold):self.execute()
        self.assertEqual(sum(a=='put-object' for _,a,_ in self.calls),1)

    def test_invalid_inputs_make_no_requests(self):
        mutations=[('size',1),('sha256','b'*64),('region','us-east-1'),('key','control/other'),('key','control/trial/../other'),('valid_until','2026-11-25T00:00:00Z'),('retain_until','2026-11-24T00:00:00Z'),('enable_upload',False),('actor','arn:aws:iam::999999999999:user/x'),('aws','relative'),('not_before','2026-11-23T00:00:00+00:00')]
        original=copy.deepcopy(self.p)
        for key,value in mutations:
            with self.subTest(key=key,value=value):
                self.p=copy.deepcopy(original);self.p[key]=value;self.calls=[]
                with self.assertRaises(m.Hold):self.execute()
                self.assertEqual(self.calls,[])
        self.p=original;self.now+=dt.timedelta(hours=2)
        with self.assertRaises(m.Hold):self.execute()
        self.assertEqual(self.calls,[])

    def test_wrong_identity_or_unversioned_bucket_prevents_intent(self):
        original=self.aws_fake
        for phase in ('identity','versioning'):
            def fake(p,s,a,args):
                if phase=='identity' and a=='get-caller-identity':return {'Account':p['account'],'Arn':p['actor']+'x'}
                if phase=='versioning' and a=='get-bucket-versioning':return {'Status':'Suspended'}
                return original(p,s,a,args)
            with self.assertRaises(m.Hold):m.store(self.p,self.source,self.receipt,True,fake,lambda:self.now)
            self.assertFalse(self.receipt.exists())

    def test_unknown_put_metadata_not_accepted(self):
        original=self.aws_fake
        def fake(p,s,a,args):
            if a=='put-object':return {'VersionId':'null','ServerSideEncryption':'AES256'}
            return original(p,s,a,args)
        with self.assertRaises(m.Hold):m.store(self.p,self.source,self.receipt,True,fake,lambda:self.now)
        self.assertEqual(m.strict(self.receipt.read_bytes())['status'],'UNKNOWN')

    def test_non_public_or_duplicate_json_rejected(self):
        for raw in (b'{"PrivateKey":"NEVER_UPLOAD"}',b'{"a":1,"a":2}',b'x'*(m.MAX+1)):
            self.source.write_bytes(raw)
            with self.assertRaises(m.Hold):self.execute()
            self.assertEqual(self.calls,[])

    def test_window_rechecked_after_durable_intent_before_put(self):
        original=m.write_receipt
        for at in ('2026-11-23T00:01:00Z','2026-11-23T00:01:01Z','2026-11-22T23:58:59Z'):
            with self.subTest(at=at):
                self.receipt.unlink(missing_ok=True);self.calls=[]
                self.now=dt.datetime(2026,11,23,tzinfo=dt.timezone.utc)
                def delayed(path,record,initial=False):
                    original(path,record,initial)
                    if initial:self.now=m.stamp(at)
                m.write_receipt=delayed
                try:
                    with self.assertRaises(m.Hold):self.execute()
                finally:m.write_receipt=original
                r=m.strict(self.receipt.read_bytes())
                self.assertEqual(r['status'],'INTENT')
                self.assertEqual(r['not_sent_reason'],'WINDOW_EXPIRED_BEFORE_PUT')
                self.assertFalse(any(a=='put-object' for _,a,_ in self.calls))

    def test_larger_object_cannot_pass_range_readback(self):
        self.failure='larger-object'
        with self.assertRaises(m.Hold):self.execute()
        self.assertEqual(m.strict(self.receipt.read_bytes())['status'],'STORED')
        self.assertFalse(any(a=='get-object' for _,a,_ in self.calls))
        self.failure='';self.execute()
        self.assertEqual(sum(a=='put-object' for _,a,_ in self.calls),1)


if __name__=='__main__':unittest.main()
