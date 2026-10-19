from pathlib import Path
import subprocess as sp,json,sys,datetime
OUT=Path(__file__).resolve().parent
PG=Path(r'C:\Users\user\.cache\labbit-lbt174-pg16')
result={'startedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'postgresStartedHere':False}
started=False
try:
 with (OUT/'db-postgres-control.log').open('ab') as log:sp.run([str(PG/'pgsql/bin/pg_ctl.exe'),'-D',str(PG/'data'),'-l',str(PG/'postgres.log'),'-o','-h 127.0.0.1 -p 15432','-w','start'],stdout=log,stderr=sp.STDOUT,check=True)
 started=True;result['postgresStartedHere']=True
 p=sp.run([sys.executable,'-X','utf8',str(OUT/'verify.py'),'db']);result['exitCode']=p.returncode
finally:
 if started:
  with (OUT/'db-postgres-cleanup.log').open('wb') as log:
   query=sp.run([str(PG/'pgsql/bin/psql.exe'),'-h','127.0.0.1','-p','15432','-U','postgres','-d','postgres','-Atc',"select count(*) from pg_database where datname like 'labbit_it_%'"],stdout=log,stderr=sp.STDOUT);result['postgresQueryExit']=query.returncode
  with (OUT/'db-postgres-control.log').open('ab') as log:stop=sp.run([str(PG/'pgsql/bin/pg_ctl.exe'),'-D',str(PG/'data'),'-m','fast','-w','stop'],stdout=log,stderr=sp.STDOUT);result['postgresStopExit']=stop.returncode
 result['endedUTC']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 (OUT/'db-run-result.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf8')
 print(json.dumps(result),flush=True)
