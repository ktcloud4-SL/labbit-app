"""Portable pre-PR validation; writes raw logs, run metadata and source identity.

Required: LABBIT_EVIDENCE_REPO, LABBIT_EVIDENCE_OUT. Go/GCC/git on PATH.
Web: LABBIT_EVIDENCE_PNPM and LABBIT_EVIDENCE_NODE_BIN (or pnpm/node on PATH).
Contracts: pinned validators on PYTHONPATH or LABBIT_EVIDENCE_VALIDATORS.
Cloud: explicit OS_CLOUD, LABBIT_PROVIDER_CONFIG_FILE and SSH key file.
"""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import json
import os
import subprocess
import sys

REPO = Path(os.environ['LABBIT_EVIDENCE_REPO']).resolve(strict=True)
OUT = Path(os.environ['LABBIT_EVIDENCE_OUT']).resolve()
OUT.mkdir(parents=True, exist_ok=True)

def now():
    return datetime.now(timezone.utc).isoformat()

def save(name, value):
    (OUT/name).write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')

def git(*args):
    return subprocess.check_output(['git',*args], cwd=REPO, stderr=subprocess.DEVNULL).decode('utf-8').strip()

def snapshot(label):
    names = sorted(set(git('diff','--name-only','origin/main...HEAD').splitlines()) | set(git('diff','--name-only','HEAD').splitlines()) | set(git('ls-files','--others','--exclude-standard').splitlines()))
    files=[]
    for name in names:
        path=REPO/name
        if path.is_file():
            files.append({'path':name,'sha256Bytes':hashlib.sha256(path.read_bytes()).hexdigest(),'gitBlob':git('hash-object','--path',name,name)})
    value={'recordedAt':now(),'head':git('rev-parse','HEAD'),'branch':git('branch','--show-current'),'originMain':git('rev-parse','origin/main'),'mergeBase':git('merge-base','HEAD','origin/main'),'localStatus':git('status','--porcelain'),'files':files}
    save('source-'+label+'.json',value)
    return value

def clean_env():
    env=os.environ.copy()
    for key in env:
        if key.startswith(('LABBIT_OPENSTACK_', 'LABBIT_KTCLOUD_')) and (key.endswith('_TEST') or key.endswith('_AUDIT') or key.endswith('_INTEGRATION')):
            env[key]='0'
    env['LABBIT_OPENSTACK_INTEGRATION']='0'
    return env

def run(name, cmd, *, env=None, cwd=REPO, required=True):
    record={'command':cmd,'startedAt':now()}
    with (OUT/(name+'.log')).open('wb') as stdout, (OUT/(name+'.stderr.txt')).open('wb') as stderr:
        result=subprocess.run(cmd,cwd=cwd,env=env or clean_env(),stdout=stdout,stderr=stderr)
    record.update(endedAt=now(),exitCode=result.returncode)
    counts={'pass':0,'skip':0,'fail':0,'packagesPass':0}
    for line in (OUT/(name+'.log')).read_text(encoding='utf-8',errors='replace').splitlines():
        try: item=json.loads(line)
        except (ValueError,TypeError): continue
        if item.get('Test') and item.get('Action') in ('pass','skip','fail'): counts[item['Action']]+=1
        elif item.get('Action')=='pass' and item.get('Package'): counts['packagesPass']+=1
    record['goEvents']=counts
    save(name+'-run.json',record)
    print(json.dumps({'task':name,'exitCode':result.returncode,'goEvents':counts}),flush=True)
    if required and result.returncode: raise SystemExit(result.returncode)
    return result.returncode

def auto():
    env=clean_env()
    files=git('ls-files','*.go').splitlines()
    files+=git('ls-files','--others','--exclude-standard','*.go').splitlines()
    failures=[]
    # Git on Windows checks out CRLF; compare LF-normalized bytes without
    # rewriting the entire repository merely to satisfy a Linux fmt target.
    for name in files:
        content=(REPO/name).read_bytes().replace(b'\r\n',b'\n')
        result=subprocess.run(['gofmt'],input=content,capture_output=True)
        if result.returncode or result.stdout!=content: failures.append(name)
    save('gofmt-run.json',{'checked':len(files),'lineEndings':'LF-normalized comparison','unformatted':failures,'exitCode':int(bool(failures))})
    print(json.dumps({'task':'gofmt','checked':len(files),'unformatted':failures}),flush=True)
    if failures: raise SystemExit(1)
    run('go-vet',['go','vet','-mod=readonly','./...'],env=env)
    run('go-unit',['go','test','-mod=readonly','-json','-count=1','-timeout=5m','./...'],env=env)
    run('go-build',['go','build','-mod=readonly','./...'],env=env)
    run('go-race',['go','test','-mod=readonly','-race','-json','-count=1','-timeout=5m','./internal/connector/vmaccess','./internal/connector/provider/openstack','./internal/connector/app','./internal/connector/terminal','./internal/connector/wss','./internal/connector/mock','./internal/connector/preview'],env=env)
    selected='^(TestManagedPTYCanceledSetupReleasesOutputWorkers|TestReplacedDataReaderCannotDispatchBufferedFrames|TestOldInputFailureCannotEndReplacement|TestDataInputAndResizeFailuresCloseOnlyTheirSession|TestReplacementDataClientStopsPreviousReconnectOwner|TestPreviewGatewayConcurrentFrames|TestPreview_SequentialTunnels_SameSession|TestCanceledChannelLeavesActivePTYUsable|TestCanceledOpenClosesLateChannelAndReusesTransport|TestStalledOpenDoesNotAccumulateCanceledSSHRequests|TestTerminalShutdownRejectsDelayedOpensAndEndsOnce)$'
    run('regression-repeat',['go','test','-mod=readonly','-race','-json','-count=20','-timeout=5m','-run',selected,'./internal/connector/terminal','./internal/connector/vmaccess','./internal/connector/mock','./internal/connector/preview'],env=env)

def web():
    env=clean_env()
    if os.environ.get('LABBIT_EVIDENCE_NODE_BIN'):
        env['PATH']=os.environ['LABBIT_EVIDENCE_NODE_BIN']+os.pathsep+env['PATH']
    pnpm=os.environ.get('LABBIT_EVIDENCE_PNPM','pnpm')
    lock=REPO/'web/pnpm-lock.yaml'
    generated=not lock.exists()
    original=hashlib.sha256((REPO/'web/package-lock.json').read_bytes()).hexdigest()
    try:
        if generated: run('web-lock-import',[pnpm,'import'],env=env,cwd=REPO/'web')
        run('web-install',[pnpm,'install','--frozen-lockfile'],env=env,cwd=REPO/'web')
        for name in ['typecheck','lint','test','build']:
            run('web-'+name,[pnpm,'run',name],env=env,cwd=REPO/'web')
    finally:
        if generated and lock.exists(): lock.unlink()
        after=hashlib.sha256((REPO/'web/package-lock.json').read_bytes()).hexdigest()
        save('web-lock-check.json',{'packageLockBefore':original,'packageLockAfter':after,'unchanged':original==after,'adapter':'pnpm import of tracked npm lock; frozen install; generated pnpm lock removed'})
        if original!=after: raise SystemExit('Tracked npm lock changed')

def contracts():
    if os.environ.get('LABBIT_EVIDENCE_VALIDATORS'): sys.path.insert(0,os.environ['LABBIT_EVIDENCE_VALIDATORS'])
    import yaml
    import importlib.metadata
    from openapi_spec_validator import validate
    versions={p:importlib.metadata.version(p) for p in ['PyYAML','jsonschema','openapi-spec-validator']}
    workflow=yaml.safe_load((REPO/'.github/workflows/contracts.yml').read_text(encoding='utf-8'))
    os.chdir(REPO)
    with (OUT/'contracts.log').open('w',encoding='utf-8') as log:
        original=sys.stdout
        try:
            sys.stdout=log
            validate(yaml.safe_load((REPO/'contracts/http/openapi.yaml').read_text(encoding='utf-8')))
            print('OpenAPI validation: OK')
            for step in workflow['jobs']['validate']['steps']:
                script=step.get('run','')
                if "python <<'PY'" in script:
                    exec(compile(script.split("python <<'PY'",1)[1].rsplit('\nPY',1)[0],'<current-contract-workflow>','exec'),{})
        finally: sys.stdout=original
    save('contracts-run.json',{'exitCode':0,'versions':versions,'scope':'OpenAPI and exact current workflow JSON Schema/runtime/migration scripts'})
    env=clean_env()
    if os.environ.get('LABBIT_EVIDENCE_VALIDATORS'): env['PYTHONPATH']=os.environ['LABBIT_EVIDENCE_VALIDATORS']+os.pathsep+env.get('PYTHONPATH','')
    run('reset-schema',[sys.executable,str(REPO/'contracts/connector/tests/test_reset_schema.py'),'-v'],env=env)
    print(json.dumps({'task':'contracts','exitCode':0,'versions':versions}),flush=True)

def cloud():
    env=clean_env()
    for name in ['OS_CLOUD','LABBIT_PROVIDER_CONFIG_FILE','LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE']:
        if not env.get(name): raise SystemExit('Missing explicit cloud input: '+name)
    env.update(LABBIT_OPENSTACK_INTEGRATION='1',LABBIT_OPENSTACK_PTY_CC02_TEST='1')
    codes={}
    codes['pre']=run('pre-discovery',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-run','^TestOpenStackM1Integration$','./internal/connector/provider/openstack'],env=env,required=False)
    if codes['pre']:
        save('cloud-exit-codes.json',codes)
        raise SystemExit(codes['pre'])
    try:
        codes['pty']=run('real-pty-cc02',['go','test','-mod=readonly','-race','-json','-count=1','-timeout=18m','-run','^TestOpenStackPTYCC02Integration$','./internal/connector/provider/openstack'],env=env,required=False)
    finally:
        codes['post']=run('post-discovery',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-run','^TestOpenStackM1Integration$','./internal/connector/provider/openstack'],env=env,required=False)
        save('cloud-exit-codes.json',codes)
    if any(codes.values()) or 'pty' not in codes: raise SystemExit('Cloud validation failed')

def residual():
    env=clean_env()
    for name in ['OS_CLOUD','LABBIT_PROVIDER_CONFIG_FILE']:
        if not env.get(name): raise SystemExit('Missing explicit cloud input: '+name)
    env['LABBIT_OPENSTACK_RESIDUAL_AUDIT']='1'
    harness=Path(__file__).resolve().parent/'residual_harness.go.txt'
    overlay=OUT/'residual-overlay.json'
    save('residual-overlay.json',{'Replace':{str(REPO/'internal/connector/provider/openstack/lbt148_residual_audit_test.go'):str(harness)}})
    run('fixture-residual',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-overlay',str(overlay),'-run','^TestLBT148ReadOnlyFixtureResiduals$','./internal/connector/provider/openstack'],env=env)

if __name__ == '__main__':
    mode=sys.argv[1]
    before=snapshot(mode+'-before')
    try:
        {'auto':auto,'web':web,'contracts':contracts,'cloud':cloud,'residual':residual,'snapshot':lambda:None}[mode]()
    finally:
        after=snapshot(mode+'-after')
        # Web ignored build output is excluded; any tested tracked/untracked
        # source change must be explained before a final verdict.
        save(mode+'-source-stability.json',{'unchanged':before['head']==after['head'] and before['files']==after['files'],'headBefore':before['head'],'headAfter':after['head']})
