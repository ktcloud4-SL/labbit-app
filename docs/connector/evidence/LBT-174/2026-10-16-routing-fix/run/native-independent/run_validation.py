from pathlib import Path
import subprocess as sp,os,json,hashlib,datetime,time,sys
OUT=Path(__file__).resolve().parent
ROOT=Path('C:\\Users\\user\\Documents\\ChatGPT\\3차 2\\tmp\\labbit-connector-gates-20261015')
GO=Path(r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe')
PG=Path(r'C:\Users\user\.cache\labbit-lbt174-pg16')
probe='probe' in sys.argv
PREFIX='probe' if probe else 'native'
def save(name,value):(OUT/(PREFIX+'-'+name)).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def identity():
 v=json.loads((OUT/'source-manifest.json').read_text(encoding='utf8'))
 files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in v['files']}
 return {'files':len(files),'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest(),'mismatches':sum(files[p]!=v['files'][p] for p in files)}
env=os.environ.copy();env['PATH']=str(GO.parent)+os.pathsep+env['PATH']
env.update({'LABBIT_PROVIDER_CONFIG_FILE':r'C:\Users\user\.config\openstack\clouds.yaml','OS_CLOUD':'labbit-test','LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE':str(ROOT/'.local/lbt174/local-fixture-key'),'LABBIT_LOCAL_NETWORK_RACE':'1','LABBIT_LOCAL_VALIDATION_DIR':str(OUT),'LABBIT_LOCAL_GO_SHA256':'63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445','LABBIT_LOCAL_SOURCE_SHA256':json.loads((OUT/'linux-source-archive.json').read_text())['archiveSHA256']})
if probe:env['LABBIT_LOCAL_PROBE_ONLY']='1'
args=[str(GO),'test','-overlay',str(OUT/'overlay.json'),'-json','-run','^TestLocalLinuxRaceAndInternetDiagnostics$','-count=1','-timeout=60m','./internal/connector/provider/openstack']
before=identity();assert before['mismatches']==0
result={'startedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':args,'sourceBefore':before,'overlaySHA256':{n:hashlib.sha256((OUT/n).read_bytes()).hexdigest() for n in ['overlay.json','local_probe_test.go','linux_ci.sh']},'postgresStartedHere':False}
save('run-state.json',result);start=time.monotonic();started_here=False
try:
 if not probe:
  with (OUT/'postgres-control.log').open('ab') as log:sp.run([str(PG/'pgsql/bin/pg_ctl.exe'),'-D',str(PG/'data'),'-l',str(PG/'postgres.log'),'-o','-h 127.0.0.1 -p 15432','-w','start'],stdout=log,stderr=sp.STDOUT,check=True)
  started_here=True;result['postgresStartedHere']=True
 with (OUT/(PREFIX+'-live.log')).open('wb') as log:
  p=sp.run(args,cwd=ROOT,env=env,stdout=log,stderr=sp.STDOUT,timeout=3700)
 result['exitCode']=p.returncode
except BaseException as exc:
 result['runnerExceptionType']=type(exc).__name__
 raise
finally:
 if started_here:
  with (OUT/'postgres-cleanup.log').open('wb') as log:
   query=sp.run([str(PG/'pgsql/bin/psql.exe'),'-h','127.0.0.1','-p','15432','-U','postgres','-d','postgres','-Atc',"select count(*) from pg_database where datname like 'labbit_it_%'"],stdout=log,stderr=sp.STDOUT);result['postgresQueryExit']=query.returncode
  with (OUT/'postgres-control.log').open('ab') as log:
   stop=sp.run([str(PG/'pgsql/bin/pg_ctl.exe'),'-D',str(PG/'data'),'-m','fast','-w','stop'],stdout=log,stderr=sp.STDOUT);result['postgresStopExit']=stop.returncode
 else:result['postgresCleanupSkipped']='not_started_here'
 result.update({'finishedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'elapsedSeconds':round(time.monotonic()-start,3),'sourceAfter':identity()})
 save('run-result.json',result)
 print(json.dumps({k:v for k,v in result.items() if k not in ['command','overlaySHA256']},ensure_ascii=False),flush=True)
