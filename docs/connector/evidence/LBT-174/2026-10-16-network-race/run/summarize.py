from pathlib import Path
import json,re,hashlib,collections,sys
BASE=Path(__file__).resolve().parent
OUT=BASE if len(sys.argv)<2 else BASE/sys.argv[1]
ROOT=BASE.parent/'labbit-connector-gates-20261015'
def save(name,value):(OUT/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def events(path):
 result=[]
 for line in path.read_text(encoding='utf8',errors='replace').splitlines():
  try: row=json.loads(line)
  except ValueError:continue
  if isinstance(row,dict) and row.get('Action') in ['run','pass','fail','skip','output','start']:result.append(row)
 return result
host=events(OUT/'local-network-race.log')
observations=[]
for row in host:
 s=row.get('Output','');prefix='LBT174_SSH_OBSERVATION '
 if s.startswith(prefix):observations.append(json.loads(s[len(prefix):]))
save('ssh-observations.json',observations)
first=[v for v in observations if v['kind']=='internet-probe']
save('internet-summary.json',{'firstSeriesAttempts':len(first),'successes':sum(v['success'] for v in first),'failures':sum(not v['success'] for v in first),'observations':first,'hostEvents':dict(collections.Counter(r['Action'] for r in host))})
ci=OUT/'linux-ci.log'
if ci.exists():
 es=events(ci)
 required=set()
 for p in (ROOT/'internal/server/app').glob('*test.go'):
  required.update(re.findall(r'^func (TestC1\w+)\(',p.read_text(encoding='utf8'),re.M))
 parents={r['Test']:r['Action'] for r in es if r.get('Test') in required and r['Action'] in ['pass','fail','skip']}
 failures=[r for r in es if r['Action']=='fail'];skips=[r for r in es if r['Action']=='skip']
 raw=ci.read_text(encoding='utf8',errors='replace')
 v={'requiredParents':sorted(required),'observedParents':parents,'missing':sorted(required-parents.keys()),'nonPass':[k for k,v in parents.items() if v!='pass'],'failures':failures,'skips':skips,'eventCounts':dict(collections.Counter(r['Action'] for r in es)),'dataRaceReports':raw.count('WARNING: DATA RACE'),'vetMarker':'LBT174_LINUX_VET_PASS' in raw,'buildMarker':'LBT174_LINUX_BUILD_PASS' in raw,'raceMarker':'LBT174_LINUX_RACE_PASS' in raw,'rawSHA256':hashlib.sha256(ci.read_bytes()).hexdigest()}
 v['gatePass']=len(required)==12 and not(v['missing'] or v['nonPass'] or failures or skips or v['dataRaceReports']) and all(v[k] for k in ['vetMarker','buildMarker','raceMarker'])
 save('linux-ci-summary.json',v)
 print(json.dumps(v,ensure_ascii=False),flush=True)
if (OUT/'route-counterfactual.log').exists():
 cases=[]
 for line in (OUT/'route-counterfactual.log').read_text(encoding='utf8',errors='replace').splitlines():
  try:cases.append(json.loads(line))
  except ValueError:pass
 save('route-summary.json',{'cases':cases,'rawSHA256':hashlib.sha256((OUT/'route-counterfactual.log').read_bytes()).hexdigest()})
