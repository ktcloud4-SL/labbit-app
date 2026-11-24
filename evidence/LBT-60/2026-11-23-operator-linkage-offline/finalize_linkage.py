from pathlib import Path
import subprocess, json, hashlib, collections, gzip, shutil, re, datetime

ROOT=Path(__file__).resolve().parent.parent
OUT=ROOT/'evidence/LBT-60/2026-11-23-operator-linkage-offline'
OUT.mkdir(exist_ok=True)
BASE='753eecbff737409a4c9cc8c542f3c6baa6acf0da'
HEAD=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip()
def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT).decode().splitlines()
def write(name,obj): (OUT/name).write_text(json.dumps(obj,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
def sha(b):return hashlib.sha256(b).hexdigest()
local=set(git('diff','--name-only','HEAD')) | set(git('ls-files','--others','--exclude-standard'))
paths=set(git('diff','--name-only',BASE,'HEAD')) | local
paths={p for p in paths if not p.startswith('evidence/LBT-60/2026-11-23-operator-linkage-offline/')}
old=json.loads((ROOT/'evidence/LBT-60/2026-11-23-pr196-publication/publication-source.json').read_text(encoding='utf-8'))
prior={x['path']:x for x in old['files']}
inventory=[]
for p in sorted(paths):
 f=ROOT/p
 if not f.is_file():inventory.append({'path':p,'deleted':True});continue
 raw=f.read_bytes();norm=raw.replace(b'\r\n',b'\n');prev=prior.get(p,{})
 category='evidence / historical record' if p.startswith('evidence/') or '/evidence/' in p else 'test / regression fixture' if '_test.' in p else 'runtime/DB owning contract' if p.startswith(('runtime/','db/')) else 'docs / deployment boundary' if p.endswith(('.md','.yaml','.yml')) else 'product / operator and affected consumer'
 inventory.append({'path':p,'bytes':len(raw),'sha256':sha(raw),'canonical_lf_sha256':sha(norm),'local_delta':p in local,'category':category,'unchanged_prior_audit_canonical':prev.get('canonical_lf_sha256')==sha(norm)})
write('source-manifest.json',{'base':BASE,'head':HEAD,'branch':git('branch','--show-current')[0],'uncommitted':True,'files':inventory})
lines=['# Full base → HEAD + local/untracked inventory','','Prior whole-PR audit supports unchanged canonical source only. New local sources and impacted consumers were re-read; historical evidence is not a current live run. Ignored .local files are test binaries, disposable PG data, raw logs and harnesses, not deployment inputs.','','| Path | Scope | Treatment |','| --- | --- | --- |']
for x in inventory:
 treatment='Fresh linkage review + affected regression' if x.get('local_delta') and not x['path'].startswith('evidence/') else 'Original bytes / historical evidence only' if x['path'].startswith('evidence/') else 'Prior full audit reused; unchanged source checked' if x.get('unchanged_prior_audit_canonical') else 'Prior PR change, inspected against owning SSOT'
 lines.append(f"| `{x['path']}` | {x.get('category','deleted')} | {treatment} |")
(OUT/'diff-inventory.md').write_text('\n'.join(lines)+'\n',encoding='utf-8')
logs=sorted((ROOT/'.local').glob('linkage-*.txt'))+sorted((ROOT/'.local').glob('linkage-*.jsonl'))+sorted((ROOT/'.local').glob('linkage-linux-*.log'))
summaries={}
for p in logs:
 if p.name.endswith('.jsonl'):
  counts=collections.Counter();pkgs=collections.Counter();skips=[];failures=[]
  for line in p.read_text(encoding='utf-8-sig',errors='replace').splitlines():
   try:r=json.loads(line)
   except ValueError:continue
   if r.get('Action') in ('pass','fail','skip'):
    (counts if 'Test'in r else pkgs).update([r['Action']])
    if r.get('Test') and r['Action']=='skip':skips.append({'package':r['Package'],'test':r['Test']})
    if r['Action']=='fail':failures.append({'package':r['Package'],'test':r.get('Test')})
  summaries[p.name]={'test_events':dict(counts),'package_events':dict(pkgs),'skipped':skips,'failed':failures}
 with gzip.GzipFile(filename=str(OUT/(p.name+'.gz')),mode='wb',mtime=0) as f:f.write(p.read_bytes())
linux=collections.Counter()
for line in (ROOT/'.local/linkage-linux-serial.log').read_text(errors='replace').splitlines():
 try:r=json.loads(line)
 except ValueError:continue
 if r.get('Test') and r.get('Action') in ('pass','fail','skip'):linux.update([r['Action']])
write('test-summary.json',{'go':summaries,'linux_no_nic':dict(linux),'web':{'typecheck':0,'lint':0,'build':0,'parallel_attempts':'initial1 and retry2 EPERM failures, raw logs retained','serial_command':'npm run test -- --maxWorkers=1','serial_test_pass':493},'limits':['Go skip includes Windows-inapplicable filesystem/script tests and external fixtures','PG full suite 10 skip: Valkey absent9 and optional sample dump1; exact names retained','No AWS, Kubernetes, shared DB, Slack, actual operator keys or live pause/resume'],'source':{'base':BASE,'head':HEAD,'local_manifest':'source-manifest.json'}})
for name in ['linkage-linux-environment.json','linkage-linux-init.sh','linux_linkage.py']:
 shutil.copyfile(ROOT/'.local'/name,OUT/name)
review=Path(r'C:\Users\user\.codex\tmp')
for name in ['pr196-linkage-review-20261123.md','pr196-linkage-review-source-final-20261123.json','pr196-linkage-review-tests-final-20261123.jsonl','pr196-linkage-review-stability-final-20261123.json','pr196-linkage-readiness-hashcheck-20261123.json']:
 p=review/name
 if p.suffix=='.jsonl':
  with gzip.GzipFile(filename=str(OUT/(name+'.gz')),mode='wb',mtime=0)as f:f.write(p.read_bytes())
 else:shutil.copyfile(p,OUT/name)
write('migration-preservation.json',{'existing_sql_unchanged':all(subprocess.check_output(['git','show',f'HEAD:{p.relative_to(ROOT).as_posix()}'],cwd=ROOT).replace(b'\r\n',b'\n')==p.read_bytes().replace(b'\r\n',b'\n') for p in (ROOT/'db/migrations').glob('[0-9]*_*.sql') if not p.name.startswith('000015')),'additive_migration':'000015_worker_control_restrictions.sql','shared_database_applied':False})
GOFMT=Path(r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe')
bad=[]
for x in inventory:
 if x.get('local_delta') and x['path'].endswith('.go'):
  raw=(ROOT/x['path']).read_bytes();formatted=subprocess.check_output([str(GOFMT)],input=raw)
  if raw.replace(b'\r\n',b'\n')!=formatted:bad.append(x['path'])
write('format-validation.json',{'noncanonical_go_files':bad})
assert not bad,bad
scan=ROOT/'.local/linkage-secret-scan'
scan.mkdir(exist_ok=True)
for x in inventory:
 if x['path'].startswith('evidence/') or x.get('deleted'):continue
 dst=scan/x['path'];dst.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(ROOT/x['path'],dst)
print(json.dumps({'inventory_files':len(inventory),'local_files':sum(x.get('local_delta',False) for x in inventory),'copied_logs':len(logs),'format':'PASS'}))
