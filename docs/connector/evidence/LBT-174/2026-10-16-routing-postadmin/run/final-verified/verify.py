import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess as sp
import sys
import time

ROOT = Path('C:\\Users\\user\\Documents\\ChatGPT\\3차 2\\tmp\\labbit-connector-gates-20261015')
OUT = Path(__file__).resolve().parent
GO = Path(r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe')
ENV = os.environ.copy()
ENV['PATH'] = str(GO.parent) + os.pathsep + ENV['PATH']

def now(): return dt.datetime.now(dt.timezone.utc).isoformat()
def save(name, value): (OUT/name).write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
def git(*args): return sp.check_output(['git',*args],cwd=ROOT)
def identity():
    paths = set(git('ls-files','--','*.go','go.mod','go.sum').decode().splitlines())
    paths.update(git('ls-files','--others','--exclude-standard','--','*.go').decode().splitlines())
    files = {p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in sorted(paths)}
    return {'base':git('rev-parse','origin/main').decode().strip(),'head':git('rev-parse','HEAD').decode().strip(),'branch':git('branch','--show-current').decode().strip(),'files':files,'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()}
def run(name,cmd,cwd=ROOT,env=ENV,timeout=1800):
    before=identity(); started=now(); tick=time.monotonic()
    with (OUT/(name+'.log')).open('wb') as log:
        r=sp.run(cmd,cwd=cwd,env=env,stdout=log,stderr=sp.STDOUT,timeout=timeout)
    after=identity()
    result=dict(name=name,command=[str(x) for x in cmd],started=started,ended=now(),seconds=round(time.monotonic()-tick,3),exitCode=r.returncode,sourceSHA256=before['sourceSHA256'],sourceUnchanged=before==after)
    save(name+'-result.json',result); print(json.dumps(result,ensure_ascii=False),flush=True)
    assert before==after, 'source changed during run'
    return r.returncode
def summarize(name):
    events=[json.loads(x) for x in (OUT/(name+'.log')).read_text(encoding='utf-8').splitlines() if x.startswith('{')]
    ended=[e for e in events if e.get('Action') in ('pass','fail','skip')]
    v={'testPass':sum(e['Action']=='pass' and 'Test' in e for e in ended),'testFail':[e for e in ended if e['Action']=='fail' and 'Test' in e],'testSkip':[e for e in ended if e['Action']=='skip' and 'Test' in e],'packagePass':sum(e['Action']=='pass' and 'Test' not in e for e in ended),'packageFail':[e for e in ended if e['Action']=='fail' and 'Test' not in e]}
    save(name+'-summary.json',v); print(json.dumps({**v,'testSkip':len(v['testSkip'])}),flush=True)

mode=sys.argv[1]
if mode=='source':
    source=identity(); save('source-manifest.json',source)
    (OUT/'code-diff.patch').write_bytes(git('diff','HEAD','--','internal'))
    (OUT/'status.txt').write_bytes(git('status','--short'))
    bad=[]
    for p in source['files']:
        if not p.endswith('.go'): continue
        b=(ROOT/p).read_bytes().replace(b'\r\n',b'\n')
        f=sp.run([str(GO.parent/'gofmt.exe')],input=b,stdout=sp.PIPE,stderr=sp.PIPE)
        if f.returncode or f.stdout!=b: bad.append(p)
    save('gofmt-result.json',{'checked':len(source['files'])-2,'mismatches':bad,'normalization':'canonical LF'})
    print(json.dumps({'sourceSHA256':source['sourceSHA256'],'files':len(source['files']),'gofmtMismatches':bad}),flush=True)
    assert not bad
elif mode=='go':
    rc=run('go-test',[str(GO),'test','-count=1','-json','./...']); summarize('go-test')
    rc|=run('go-vet',[str(GO),'vet','./...'])
    rc|=run('go-build',[str(GO),'build','./...'])
    sys.exit(rc)
elif mode=='go-serial':
    rc=run('go-test-serial',[str(GO),'test','-p=1','-count=1','-json','./...']); summarize('go-test-serial')
    sys.exit(rc)
elif mode=='go-check':
    rc=run('go-vet',[str(GO),'vet','./...'])
    rc|=run('go-build',[str(GO),'build','./...'])
    sys.exit(rc)
elif mode=='timing-repeat':
    rc=run('timing-repeat',[str(GO),'test','-count=100','-json','./internal/connector/vmaccess','./internal/server/realtime','-run','^(TestTCPOpenTimeoutLateChannelAndSiblingIsolation|TestConnectorControlEventsPropagateTraceContext|TestControlLog.*)$']); summarize('timing-repeat')
    sys.exit(rc)
elif mode=='db':
    env=ENV.copy(); env['LABBIT_TEST_DATABASE_DSN']='postgres://postgres@127.0.0.1:15432/postgres?sslmode=disable'
    packages=['./internal/postgres','./internal/postgres/postgrestest','./internal/server/app','./internal/server/auth','./internal/server/httpapi']
    rc=run('postgres-vet',[str(GO),'vet','-tags=integration',*packages],env=env)
    rc|=run('postgres-integration',[str(GO),'test','-tags=integration','-p=1','-count=1','-json','-timeout=30m',*packages],env=env,timeout=2000);summarize('postgres-integration')
    sys.exit(rc)
elif mode=='race':
    env=ENV.copy(); env['CC']=r'C:\Users\user\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin\gcc.exe'
    env['PATH']=str(Path(env['CC']).parent)+os.pathsep+env['PATH'];env['CGO_ENABLED']='1'
    sys.exit(run('go-race',[str(GO),'test','-race','-json','-p=1','-count=1','./internal/connector/provider/openstack','./internal/connector/vmaccess','./internal/connector/preview','./internal/connector/app','./internal/connector/terminal','./internal/connector/filetransport','./internal/connector/wss','./tools/ktcloud-d1-acceptance','./tools/ktcloud-op02-bootstrap','./internal/server/realtime','./internal/server/preview/previewtest'],env=env))
elif mode=='web':
    pnpm=r'C:\Users\user\.cache\codex-runtimes\codex-primary-runtime\dependencies\bin\fallback\pnpm.cmd'
    rc=0
    for script in ['typecheck','lint','test','build']:
        rc|=run('web-'+script,[pnpm,'--package=npm@10.9.2','dlx','npm','run',script],cwd=ROOT/'web')
    sys.exit(rc)
elif mode in ('live','live-all'):
    env=ENV.copy()
    env.update({'LABBIT_PROVIDER_CONFIG_FILE':r'C:\Users\user\.config\openstack\clouds.yaml','OS_CLOUD':'labbit-test','LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE':str(ROOT/'.local/lbt174/local-fixture-key'),'LABBIT_OPENSTACK_SSH_USERNAME':'ubuntu','LABBIT_OPENSTACK_TEST_IMAGE':'ubuntu','LABBIT_OPENSTACK_TEST_FLAVOR':'m1.small','LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK':'sharednet1','LABBIT_OPENSTACK_TEST_SSH_CIDR':'172.16.8.1/32','LABBIT_OPENSTACK_TEST_LAB_CIDR':'198.20.0.0/24','LABBIT_OPENSTACK_PREVIEW_TRANSPORT_TEST':'1'})
    if mode=='live-all':
        for flag in ['INTEGRATION','MUTATION_TEST','M2_TEST','M3_TEST','M3_WSS_TEST','INTERNET_POLICY_TEST','FILE_TRANSPORT_TEST','PREVIEW_TEST','PTY_CC02_TEST','TERMINAL_C2_TEST','VM_ACCESS_TEST','WORKSPACE_FILE_TEST']:
            env['LABBIT_OPENSTACK_'+flag]='1'
        rc=run('local-openstack-all',[str(GO),'test','./internal/connector/provider/openstack','-run','^TestOpenStack','-json','-count=1','-timeout=120m'],env=env,timeout=7500);summarize('local-openstack-all');sys.exit(rc)
    sys.exit(run('local-openstack-final',[str(GO),'test','./internal/connector/provider/openstack','-run','^TestOpenStackPreviewTransportIntegration$','-v','-count=1','-timeout=22m'],env=env,timeout=1500))
