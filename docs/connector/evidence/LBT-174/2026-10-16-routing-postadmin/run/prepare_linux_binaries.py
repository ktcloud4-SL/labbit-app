from pathlib import Path
import datetime, hashlib, json, os, subprocess as sp, time
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
GO=Path(r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe')
manifest=json.loads((OUT/'source-manifest.json').read_text(encoding='utf8'))
def identity():
    files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in manifest['files']}
    assert files==manifest['files']
    return hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()
env=os.environ.copy();env.update({'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'})
env['PATH']=str(GO.parent)+os.pathsep+env['PATH']
jobs=[('workspace-sftp',['build','-o','workspace-sftp','./cmd/labbit-workspace-sftp']),
      ('workspacefs-tests',['test','-c','-o','workspacefs-tests','./internal/connector/workspacefs']),
      ('provider-tests',['test','-c','-o','provider-tests','./internal/connector/provider/openstack']),
      ('workspacefile-tests',['test','-c','-o','workspacefile-tests','./internal/connector/workspacefile']),
      ('credential-tests',['test','-c','-o','credential-tests','./tools/dev-auth-class-fixture'])]
results=[]
for name,args in jobs:
    args=[str(OUT/name) if x==name else x for x in args]
    before=identity();start=time.monotonic()
    with (OUT/(name+'-build.log')).open('wb') as log:
        proc=sp.run([str(GO),*args],cwd=ROOT,env=env,stdout=log,stderr=sp.STDOUT,timeout=900)
    result={'name':name,'command':[str(GO),*args],'exitCode':proc.returncode,'elapsedSeconds':round(time.monotonic()-start,3),'sourceBefore':before,'sourceAfter':identity()}
    if proc.returncode==0:result['binarySHA256']=hashlib.sha256((OUT/name).read_bytes()).hexdigest()
    results.append(result)
    (OUT/'linux-binaries-build.json').write_text(json.dumps(results,indent=2)+'\n',encoding='utf8')
    print(json.dumps(result),flush=True)
    assert proc.returncode==0
