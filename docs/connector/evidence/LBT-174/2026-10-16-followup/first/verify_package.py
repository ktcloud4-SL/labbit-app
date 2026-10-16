from pathlib import Path
import json,hashlib,gzip,subprocess as sp,zipfile,io,re,datetime as dt
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-followup'
def save(name,value):(DEST/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def read(p):
 b=p.read_bytes()
 return gzip.decompress(b) if p.suffix=='.gz' else b
manifest=json.loads((DEST/'source-manifest.json').read_text())
rebuild=OUT.parent/('lbt174-followup-patch-reconstruction-'+dt.datetime.now().strftime('%Y%m%d-%H%M%S'))
rebuild.mkdir()
archive=sp.check_output(['git','archive','--format=zip',manifest['base']],cwd=ROOT)
with zipfile.ZipFile(io.BytesIO(archive)) as z:z.extractall(rebuild)
sp.run(['git','init','-q'],cwd=rebuild,check=True)
check=sp.run(['git','apply','--check',str(DEST/'full-code-diff.patch')],cwd=rebuild,capture_output=True,check=True)
sp.run(['git','apply',str(DEST/'full-code-diff.patch')],cwd=rebuild,capture_output=True,check=True)
actual={p:hashlib.sha256((rebuild/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in manifest['files']}
differences=[p for p in actual if actual[p]!=manifest['files'][p]]
aggregate=hashlib.sha256(json.dumps(actual,sort_keys=True).encode()).hexdigest()
provenance=json.loads((DEST/'raw-provenance.json').read_text())
rawerrors=[]
for item in provenance:
 b=read(DEST/item['artifact'])
 if len(b)!=item['rawBytes'] or hashlib.sha256(b).hexdigest()!=item['rawSHA256']:rawerrors.append(item['artifact'])
save('package-verification.json',{'reconstructionRoot':str(rebuild),'base':manifest['base'],'isolatedGitInit':True,'gitApplyCheckExit':check.returncode,'filesCompared':len(actual),'mismatches':differences,'aggregate':aggregate,'matchesClaim':aggregate==manifest['sourceSHA256'],'rawArtifactsChecked':len(provenance),'rawErrors':rawerrors})
assert not differences and not rawerrors and aggregate==manifest['sourceSHA256']

selected=[('first',OUT/'local-openstack-all.log'),('verified',OUT/'verified/local-openstack-files-linux.log'),('sealed',OUT/'sealed/local-internet-diagnostic.log'),('closing',OUT/'closing/local-workspace-linux-harness.log'),('finalized',OUT/'finalized/local-internet-unmodified.log')]
cleanup=[]
for stage,p in selected:
 if not p.exists():continue
 events=[]
 for line in p.read_text(encoding='utf8').splitlines():
  if line.startswith('{'):
   try:events.append(json.loads(line))
   except json.JSONDecodeError:pass
 tests=[]
 for e in events:
  if e.get('Action') in ('pass','fail','skip') and e.get('Test') and '/' not in e['Test']:
   name=e['Test'];lines=[x.get('Output','').strip() for x in events if x.get('Test')==name]
   markers=[x for x in lines if any(y in x.lower() for y in ('absence','cleanup','resource','keypair get'))]
   tests.append({'test':name,'action':e['Action'],'cleanupAndResourceMarkers':markers})
 cleanup.append({'stage':stage,'sourceLog':p.name,'topLevel':tests})
save('cleanup-observations.json',{'scope':'Exact tracked resources of these test runs; not a global assertion about every user/cloud resource. Failing helper gates create no VM. Internet first failure still ran deferred cleanup.','cloudRuns':cleanup,'postgresResidualDBCount':(OUT/'finalized/postgres-residual.log').read_text().strip(),'postgresStopped':'server stopped' in (OUT/'finalized/postgres-stop.log').read_text()})

# Do not print actual candidate values. Compare protected inputs in memory only.
protected=[]
key=ROOT/'.local/lbt174/local-fixture-key'
if key.exists():
 b=key.read_bytes().strip();protected.append(('SSH private key complete',b))
 for part in b.splitlines():
  if len(part)>40 and not part.startswith(b'-----'):protected.append(('SSH private key body',part))
cloud=Path(r'C:\Users\user\.config\openstack\clouds.yaml')
if cloud.exists():
 import yaml
 def walk(v,path=''):
  if isinstance(v,dict):
   for k,x in v.items():
    if isinstance(x,str) and len(x)>=8 and any(q in str(k).lower() for q in ('password','secret','token','application_credential_id')):protected.append(('protected cloud '+str(k),x.encode()))
    else:walk(x,path+'/'+str(k))
  elif isinstance(v,list):
   for x in v:walk(x,path)
 walk(yaml.safe_load(cloud.read_text(encoding='utf8')))
generic=[('privateKeyHeader',rb'-----BEGIN (?:OPENSSH |RSA |EC |)PRIVATE KEY-----'),('githubToken',rb'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b'),('awsAccessKey',rb'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b')]
exact=[];candidates=[];count=0
for p in (ROOT/'docs/connector/evidence/LBT-174').rglob('*'):
 if not p.is_file():continue
 count+=1;b=read(p)
 for label,secret in protected:
  if secret and secret in b:exact.append({'path':str(p.relative_to(ROOT)),'kind':label})
 for label,pattern in generic:
  if re.search(pattern,b):candidates.append({'path':str(p.relative_to(ROOT)),'kind':label})
save('secret-screen.json',{'filesScannedIncludingHistoricalEvidence':count,'protectedValueCandidatesCompared':len(protected),'exactProtectedInputMatches':exact,'genericPatternCandidates':candidates,'limits':'Heuristic plus exact protected-input comparison; not a proof of absence of every possible secret. Candidate values are never written. Test source may contain synthetic credential literals.'})
assert not exact,exact
print(json.dumps({'reconstructedFiles':len(actual),'aggregate':aggregate,'rawArtifacts':len(provenance),'rawErrors':len(rawerrors),'evidenceFilesScanned':count,'exactSecretMatches':len(exact),'genericCandidates':candidates},ensure_ascii=False))
