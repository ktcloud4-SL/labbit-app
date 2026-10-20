"""Offline guard regressions. Writes only to an OS temporary directory outside the package."""
import hashlib,importlib.util,json,os,pathlib,shutil,subprocess,sys,tempfile
sys.dont_write_bytecode=True
root=pathlib.Path(__file__).resolve().parent

def snapshot():
    return {p.relative_to(root).as_posix():hashlib.sha256(p.read_bytes()).hexdigest()
            for p in root.rglob('*') if p.is_file()}

def run(path,env=None):
    return subprocess.run([sys.executable,'-B',str(path),'nonexistent-input'],
                          env=env,capture_output=True,timeout=30)

before=snapshot()
checks={}
with tempfile.TemporaryDirectory(prefix='labbit-c3-guard-') as directory:
    temp=pathlib.Path(directory)
    spec=importlib.util.spec_from_file_location('c3_run_output',root/'run_output.py')
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    previous=os.environ.get('C3_EVIDENCE_OUTPUT_DIR')
    try:
        fresh=temp/'fresh';os.environ['C3_EVIDENCE_OUTPUT_DIR']=str(fresh)
        result=module.reserve_output('result.json');result.write_bytes(b'untouched')
        try:module.reserve_output('result.json')
        except FileExistsError:pass
        else:raise AssertionError('existing directory accepted')
        assert result.read_bytes()==b'untouched'
        checks['freshExclusiveOutputAndExistingBytesPreserved']=True
        for forbidden in (root,root/'forbidden-child'):
            os.environ['C3_EVIDENCE_OUTPUT_DIR']=str(forbidden)
            try:module.reserve_output('result.json')
            except ValueError:pass
            else:raise AssertionError('package directory accepted')
        checks['packageAndDescendantRejected']=True
        env=dict(os.environ,C3_EVIDENCE_OUTPUT_DIR=str(fresh));env.pop('C3_PASSWORD',None)
        for name in ('run-admin.py','run-internal-api.py','run-owned-api.py',
                     'run-matrix.py','run-private-collector.py'):
            outcome=run(root/name,env)
            assert outcome.returncode and b'FileExistsError' in outcome.stderr,name
            assert b'KeyError' not in outcome.stderr,name
        checks['fiveWrappersRejectExistingBeforeCredentialsOrSSH']=True
        outcome=run(root/'verify-evidence.py')
        assert outcome.returncode and snapshot()==before
        checks['historicalWriterDisabledWithoutWrites']=True
        outcome=run(root/'verify-package.py')
        assert outcome.returncode==0,outcome.stderr
        assert snapshot()==before
        checks['offlineVerifierReadOnlyAndNoBytecode']=True
        checks['offlineVerifierOutput']=json.loads(outcome.stdout)
        copy=temp/'tampered';shutil.copytree(root,copy)
        target=copy/'api-matrix.json';data=json.loads(target.read_text(encoding='utf-8'))
        next(iter(data['cases'].values()))['status']=599
        target.write_text(json.dumps(data),encoding='utf-8')
        outcome=run(copy/'verify-package.py')
        assert outcome.returncode and b'api-matrix.json' in outcome.stderr
        assert result.read_bytes()==b'untouched' and snapshot()==before
        checks['tamperedEvidenceRejectedOriginalUnchanged']=True
    finally:
        if previous is None:os.environ.pop('C3_EVIDENCE_OUTPUT_DIR',None)
        else:os.environ['C3_EVIDENCE_OUTPUT_DIR']=previous
print(json.dumps(checks))
