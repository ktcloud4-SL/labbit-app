from pathlib import Path
import hashlib,json,subprocess as sp,shutil
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
OLD=OUT.parent/'lbt174-network-race-20261016'
def git(*args):return sp.check_output(['git',*args],cwd=ROOT).decode().splitlines()
paths=set(git('ls-files','--','*.go','go.mod','go.sum','runtime/*','contracts/*'))|set(git('ls-files','--others','--exclude-standard','--','*.go'))
files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in sorted(paths)}
value={'base':git('rev-parse','origin/main')[0],'head':git('rev-parse','HEAD')[0],'branch':git('branch','--show-current')[0],'files':files,'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()}
(OUT/'before-source.json').write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
(OUT/'before-status.txt').write_text('\n'.join(git('status','--short'))+'\n',encoding='utf8')
for name in ['go1.27.1.linux-amd64.tar.gz','linux-go-download.json','linux_ci.sh']:
 shutil.copy2(OLD/name,OUT/name)
shutil.copy2(OLD/'linux-v3/local_probe_test.go',OUT/'local_probe_test.go')
print(json.dumps({'beforeFiles':len(files),'beforeSource':value['sourceSHA256']}))
