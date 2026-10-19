import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

out = Path(__file__).resolve().parent
repo = out.parent / 'labbit-connector-gates-20261015'
manifest = json.loads((out.parent/'lbt174-followup-20261016/finalized/source-manifest.json').read_text(encoding='utf-8'))
def identity():
    return {p:hashlib.sha256((repo/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in manifest['files']}
before = identity()
env = os.environ.copy()
env['CGO_ENABLED'] = '1'
env['CC'] = 'C:/Users/user/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin/gcc.exe'
command = ['go','test','-race','./internal/connector/provider/openstack','-run','^TestWorkspaceFixtureExec','-count=10','-v','-timeout=90s']
started = time.time()
result = subprocess.run(command,cwd=repo,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
(out/'finalized-workspace-exec-race.log').write_bytes(result.stdout)
after = identity()
report = {'command':command,'exit':result.returncode,'seconds':round(time.time()-started,3),'sourceSHA256':manifest['sourceSHA256'],'source_files':len(before),'source_matches_manifest':before==manifest['files'],'source_unchanged':before==after,'log':'finalized-workspace-exec-race.log','log_sha256':hashlib.sha256(result.stdout).hexdigest(),'scope':'Finalized working-tree source; real loopback SSH peer only; no cloud calls.'}
(out/'finalized-workspace-exec-result.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(report,ensure_ascii=False,indent=2))
assert result.returncode == 0 and before == after == manifest['files']
