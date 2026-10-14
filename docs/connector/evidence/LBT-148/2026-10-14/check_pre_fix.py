from pathlib import Path
import json
import os
import subprocess
import sys
import zipfile
repo=Path(os.environ['LABBIT_EVIDENCE_REPO'])
out=Path(os.environ['LABBIT_EVIDENCE_OUT'])
out.mkdir(parents=True,exist_ok=True)
def old_source(path, archived):
    result=subprocess.run(['git','show','f3fa76f:'+path],cwd=repo,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
    if result.returncode==0: return result.stdout
    # The old pre-rebase object may be unavailable after squash merge.
    with zipfile.ZipFile(Path(__file__).resolve().parent/'validation-logs.zip') as archive:
        return archive.read('negative-logs/'+archived)
source=out/'pre-fix-ssh-pty.go'
source.write_bytes(old_source('internal/connector/terminal/ssh_pty.go','pre-fix-ssh-pty.go'))
overlay=out/'pre-fix-overlay.json'
overlay.write_text(json.dumps({'Replace':{str(repo/'internal/connector/terminal/ssh_pty.go'):str(source)}}),encoding='utf-8')
cmd=['go','test','-mod=readonly','-race','-json','-count=2','-timeout=90s','-overlay',str(overlay),'-run','^TestManagedPTYCanceledSetupReleasesOutputWorkers$','./internal/connector/terminal']
with (out/'pre-fix-async.log').open('wb') as stdout, (out/'pre-fix-async.stderr.txt').open('wb') as stderr:
    result=subprocess.run(cmd,cwd=repo,stdout=stdout,stderr=stderr)
text=(out/'pre-fix-async.log').read_text(encoding='utf-8',errors='replace')+(out/'pre-fix-async.stderr.txt').read_text(encoding='utf-8',errors='replace')
record={'source':'f3fa76f:internal/connector/terminal/ssh_pty.go','command':cmd,'exitCode':result.returncode,'raceReproduced':'DATA RACE' in text,'leakReproduced':'output/Wait worker leaked' in text,'expectedNegativeTest':True}
(out/'pre-fix-async-run.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8')
print(json.dumps(record))
async_confirmed=result.returncode!=0 and (record['raceReproduced'] or record['leakReproduced'])
source=out/'pre-fix-preview-gateway.go'
source.write_bytes(old_source('internal/connector/mock/preview_gateway.go','pre-fix-preview-gateway.go'))
overlay=out/'pre-fix-preview-overlay.json'
overlay.write_text(json.dumps({'Replace':{str(repo/'internal/connector/mock/preview_gateway.go'):str(source)}}),encoding='utf-8')
cmd=['go','test','-mod=readonly','-race','-json','-count=2','-timeout=90s','-overlay',str(overlay),'-run','^TestPreviewGatewayConcurrentFrames$','./internal/connector/mock']
with (out/'pre-fix-preview.log').open('wb') as stdout, (out/'pre-fix-preview.stderr.txt').open('wb') as stderr:
    result=subprocess.run(cmd,cwd=repo,stdout=stdout,stderr=stderr)
text=(out/'pre-fix-preview.log').read_text(encoding='utf-8',errors='replace')+(out/'pre-fix-preview.stderr.txt').read_text(encoding='utf-8',errors='replace')
record={'source':'f3fa76f:internal/connector/mock/preview_gateway.go','command':cmd,'exitCode':result.returncode,'raceReproduced':'DATA RACE' in text,'panicReproduced':'concurrent write to websocket connection' in text,'expectedNegativeTest':True}
(out/'pre-fix-preview-run.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8')
print(json.dumps(record))
sys.exit(0 if async_confirmed and result.returncode!=0 and (record['raceReproduced'] or record['panicReproduced']) else 1)
