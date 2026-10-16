from pathlib import Path
import datetime,hashlib,json,os,shutil,subprocess as sp,sys
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
OLDWT=OUT/'latest-main-integration'
NEW=OUT/'latest-main-reconcile-integration'
CHECK=OUT/'latest-main-reconcile-check'
PG=Path(r'C:\Users\user\.cache\labbit-lbt174-pg16')
HEAD='1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4'
CHECK.mkdir(exist_ok=True)
def save(name,v):(CHECK/name).write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def git(*a,cwd=ROOT):return sp.check_output(['git',*a],cwd=cwd)
assert not NEW.exists(),'do not overwrite existing checkout'
sp.run(['git','worktree','add','--detach',str(NEW),HEAD],cwd=ROOT,check=True,stdout=sp.PIPE,stderr=sp.STDOUT)
patch=git('diff','HEAD','--','internal','runtime','contracts')
(CHECK/'branch.patch').write_bytes(patch)
r=sp.run(['git','apply','--3way','--index','-'],cwd=NEW,input=patch,stdout=sp.PIPE,stderr=sp.STDOUT)
(CHECK/'patch-apply.log').write_bytes(r.stdout);assert r.returncode==0
untracked=git('ls-files','--others','--exclude-standard','--','*.go').decode().splitlines()
assert len(untracked)==19
for rel in untracked:
 p=NEW/rel;p.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(ROOT/rel,p)
for name in ['verify.py','final_checks.py','build_sources.py']:
 raw=(OUT/'latest-main-check'/name).read_text(encoding='utf8')
 assert repr(str(OLDWT)) in raw
 raw=raw.replace(repr(str(OLDWT)),repr(str(NEW)))
 (CHECK/name).write_text(raw,encoding='utf8')
paths=set(git('ls-files','--','*.go','go.mod','go.sum','runtime/*','contracts/*',cwd=NEW).decode().splitlines())|set(untracked)
files={p:hashlib.sha256((NEW/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in sorted(paths)}
save('source-manifest.json',{'head':HEAD,'base':HEAD,'branch':'','files':files,'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()})
web=json.loads((OUT/'latest-main-check/web-source-identity.json').read_text(encoding='utf8'))
webfiles={p:hashlib.sha256((NEW/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in web['files']}
assert webfiles==web['files']
assert git('rev-parse','HEAD:web',cwd=NEW).decode().strip()==web['headWebTree']
save('web-identity-bridge.json',{'head':HEAD,'comparedWebFiles':len(webfiles),'mismatches':[],'webTree':web['headWebTree'],'evidence':'../latest-main-check/web-*','reason':'tracked web66 source+dependencies/config/tree identical to completed ee05485 Web typecheck/lint/16files217tests/build'})
save('setup.json',{'head':HEAD,'patchExit':r.returncode,'untrackedGoCopied':len(untracked),'primaryHEAD':git('rev-parse','HEAD').decode().strip(),'primaryStaged':git('diff','--cached','--name-only').decode().splitlines()})
print(json.dumps({'setup':'PASS','head':HEAD,'files':len(files)}),flush=True)
env=os.environ.copy();env['PYTHONPATH']=str(OUT.parent/'lbt174-validator-deps')
for script,args in [('verify.py',['go']),('final_checks.py',[])]:
 sp.run([sys.executable,'-X','utf8',str(CHECK/script),*args],env=env,check=True)
owned=False;state={'startedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat()}
try:
 with (CHECK/'postgres-control.log').open('ab') as log:
  sp.run([str(PG/'pgsql/bin/pg_ctl.exe'),'-D',str(PG/'data'),'-l',str(PG/'postgres.log'),'-o','-h 127.0.0.1 -p 15432','-w','start'],stdout=log,stderr=sp.STDOUT,check=True)
 owned=True
 result=sp.run([sys.executable,'-X','utf8',str(CHECK/'verify.py'),'db'],env=env,check=False)
 state['exitCode']=result.returncode
finally:
 if owned:
  with (CHECK/'postgres-cleanup.log').open('wb') as log:
   query=sp.run([str(PG/'pgsql/bin/psql.exe'),'-h','127.0.0.1','-p','15432','-U','postgres','-d','postgres','-Atc',"select count(*) from pg_database where datname like 'labbit_it_%'"],stdout=log,stderr=sp.STDOUT)
   state['queryExit']=query.returncode
  with (CHECK/'postgres-control.log').open('ab') as log:
   stop=sp.run([str(PG/'pgsql/bin/pg_ctl.exe'),'-D',str(PG/'data'),'-m','fast','-w','stop'],stdout=log,stderr=sp.STDOUT)
   state['stopExit']=stop.returncode
 state['finishedUTC']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 after={p:hashlib.sha256((NEW/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in files}
 state['sourceUnchanged']=after==files
 save('database-owner-result.json',state)
print(json.dumps(state),flush=True)
assert state['exitCode']==0 and state['stopExit']==0 and state['sourceUnchanged']
assert (CHECK/'postgres-cleanup.log').read_text().strip()=='0'
