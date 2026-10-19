from pathlib import Path
import json,hashlib,subprocess as sp,gzip,shutil,datetime as dt
ROOT=Path(__file__).resolve().parent.parent/'labbit-connector-gates-20261015'
OUT=Path(__file__).resolve().parent
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-followup'
DEST.mkdir(parents=True,exist_ok=True)
def save(p,v):p.write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def git(*args):return sp.check_output(['git','-c','core.quotePath=false',*args],cwd=ROOT)
paths=set(git('ls-files','--','*.go','go.mod','go.sum').decode().splitlines())
paths.update(git('ls-files','--others','--exclude-standard','--','*.go').decode().splitlines())
files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in sorted(paths)}
identity={'base':git('rev-parse','origin/main').decode().strip(),'head':git('rev-parse','HEAD').decode().strip(),'branch':git('branch','--show-current').decode().strip(),'files':files,'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()}
save(DEST/'source-manifest.json',identity)
patch=git('diff','--no-ext-diff','--binary','HEAD','--','internal')
new=git('ls-files','--others','--exclude-standard','--','*.go').decode().splitlines()
for path in new:
 b=(ROOT/path).read_bytes().replace(b'\r\n',b'\n'); assert b.endswith(b'\n'),path
 lines=b.decode().splitlines()
 patch+=('diff --git a/'+path+' b/'+path+'\nnew file mode 100644\n--- /dev/null\n+++ b/'+path+'\n@@ -0,0 +1,'+str(len(lines))+' @@\n'+''.join('+'+x+'\n' for x in lines)).encode()
(DEST/'full-code-diff.patch').write_bytes(patch)
changed=git('diff','--name-only','HEAD','--','internal').decode().splitlines()+new
save(DEST/'code-inventory.json',[{'path':p,'sha256LF':files[p]} for p in sorted(changed)])
provenance=[]
for label,folder in [('first',OUT),('intermediate',OUT/'final'),('verified',OUT/'verified'),('sealed',OUT/'sealed'),('closing',OUT/'closing'),('finalized',OUT/'finalized'),('post-package',OUT/'post-package'),('independent-review',OUT.parent/'lbt174-followup-independent-0051d8d')]:
 target=DEST/label;target.mkdir(exist_ok=True)
 for p in folder.glob('*'):
  if not p.is_file() or p.suffix not in ('.log','.json','.jsonl','.txt','.md','.py','.go','.patch'):continue
  b=p.read_bytes()
  if p.suffix in ('.log','.jsonl') and len(b)>24000:
   name=p.name+'.gz';(target/name).write_bytes(gzip.compress(b,mtime=0))
  else:name=p.name+'.txt' if p.suffix=='.go' else p.name;(target/name).write_bytes(b)
  provenance.append({'source':label+'/'+p.name,'artifact':label+'/'+name,'rawBytes':len(b),'rawSHA256':hashlib.sha256(b).hexdigest()})
 # Gather complete JSONL event summaries independently of runner source guard.
 for p in folder.glob('*.log'):
  events=[]
  for line in p.read_text(encoding='utf8',errors='replace').splitlines():
   if line.startswith('{'):
    try:events.append(json.loads(line))
    except json.JSONDecodeError:pass
  ended=[e for e in events if e.get('Action') in ('pass','fail','skip')]
  if not ended:continue
  save(target/(p.stem+'-events.json'),{'testPass':sum(e['Action']=='pass' and 'Test' in e for e in ended),'testFail':[e for e in ended if e['Action']=='fail' and 'Test' in e],'testSkip':[e for e in ended if e['Action']=='skip' and 'Test' in e],'packagePass':sum(e['Action']=='pass' and 'Test' not in e for e in ended),'packageFail':[e for e in ended if e['Action']=='fail' and 'Test' not in e],'testResults':[e for e in ended if 'Test' in e]})
save(DEST/'raw-provenance.json',provenance)
(DEST/'working-tree.txt').write_bytes(git('status','--short','--untracked-files=all'))
print(json.dumps({'sourceSHA256':identity['sourceSHA256'],'files':len(files),'changedGoFiles':len(changed),'artifacts':len(provenance)},ensure_ascii=False))
