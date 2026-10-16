from pathlib import Path
import datetime,hashlib,json,subprocess as sp,sys
OUT=Path(__file__).resolve().parent
final='final' in sys.argv
def sha(b):return hashlib.sha256(b).hexdigest()
for name in ['latest-main','latest-main-reconcile']:
 root=OUT/(name+'-integration');dest=OUT/(name+'-check')
 paths=sp.check_output(['git','ls-files','--','db/migrations/*.sql'],cwd=root).decode().splitlines()
 rows=[]
 for rel in paths:
  actual=(root/rel).read_bytes();head=sp.check_output(['git','show','HEAD:'+rel],cwd=root)
  rows.append({'path':rel,'workingRawSHA256':sha(actual),'headRawSHA256':sha(head),'workingCanonicalLFSHA256':sha(actual.replace(b'\r\n',b'\n')),'headCanonicalLFSHA256':sha(head.replace(b'\r\n',b'\n')),'rawIdenticalOrLFtoCRLFOnly':actual==head or actual==head.replace(b'\n',b'\r\n')})
 assert len(rows)==9 and all(r['workingCanonicalLFSHA256']==r['headCanonicalLFSHA256'] and r['rawIdenticalOrLFtoCRLFOnly'] for r in rows)
 value={'observedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'head':sp.check_output(['git','rev-parse','HEAD'],cwd=root).decode().strip(),'phase':'final after DB' if final else ('during actual PG integration' if 'reconcile' in name else 'after completed PG integration'),'workingDiff':sp.check_output(['git','diff','HEAD','--','db/migrations'],cwd=root).decode(),'files':rows,'rawByteDifferencesFromHEAD':sum(r['workingRawSHA256']!=r['headRawSHA256'] for r in rows),'limits':'Go/runtime/contracts manifest excludes SQL; this independent observation records SQL exact HEAD and current files. During-run observation is not a pre-start hash.'}
 assert value['workingDiff']==''
 p=dest/'migration-source-identity.json'
 if final:
  previous=json.loads(p.read_text(encoding='utf8'))
  value['previousObservationUTC']=previous['observedUTC'];value['unchangedSincePreviousObservation']=previous['files']==rows
  assert value['unchangedSincePreviousObservation']
  p=dest/'migration-source-final-identity.json'
 p.write_text(json.dumps(value,indent=2)+'\n',encoding='utf8')
 print(json.dumps({'checkout':name,'files':len(rows),'rawDifferences':value['rawByteDifferencesFromHEAD'],'workingDiffEmpty':True,'phase':value['phase']}),flush=True)
