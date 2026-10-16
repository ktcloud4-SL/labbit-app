from pathlib import Path
import json,hashlib,gzip,re
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-network-race'
def save(name,value):(DEST/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def read(p):
 b=p.read_bytes()
 return gzip.decompress(b) if p.suffix=='.gz' else b
raw=json.loads((DEST/'raw-provenance.json').read_text(encoding='utf8'))
errors=[]
for item in raw:
 b=read(DEST/item['artifact'])
 if len(b)!=item.get('artifactUncompressedBytes',item['rawBytes']) or hashlib.sha256(b).hexdigest()!=item.get('artifactUncompressedSHA256',item['rawSHA256']):errors.append(item['artifact'])
old=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-followup'
old_seal=json.loads((old/'package-checksums.json').read_text(encoding='utf8'))
old_errors=[p for p,v in old_seal['files'].items() if hashlib.sha256((old/p).read_bytes()).hexdigest()!=v]
manifest=json.loads((OUT/'source-manifest.json').read_text(encoding='utf8'))
files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in manifest['files']}
source_errors=[p for p,v in files.items() if v!=manifest['files'][p]]
save('package-verification.json',{'rawArtifacts':len(raw),'rawErrors':errors,'previousFrozenFiles':len(old_seal['files']),'previousFrozenErrors':old_errors,'sourceFiles':len(files),'sourceErrors':source_errors,'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest(),'goSourcesInEvidence':len(list(DEST.rglob('*.go')))})
assert not errors and not old_errors and not source_errors and not list(DEST.rglob('*.go'))
protected=[]
key=ROOT/'.local/lbt174/local-fixture-key'
if key.exists():
 b=key.read_bytes().strip();protected.append(('SSH private key complete',b))
 for part in b.splitlines():
  if len(part)>40 and not part.startswith(b'-----'):protected.append(('SSH private key body',part))
cloud=Path(r'C:\Users\user\.config\openstack\clouds.yaml')
if cloud.exists():
 import yaml
 def walk(v):
  if isinstance(v,dict):
   for k,x in v.items():
    if isinstance(x,str) and len(x)>=8 and any(q in str(k).lower() for q in ('password','secret','token','application_credential_id')):protected.append(('protected cloud '+str(k),x.encode()))
    else:walk(x)
  elif isinstance(v,list):
   for x in v:walk(x)
 walk(yaml.safe_load(cloud.read_text(encoding='utf8')))
generic=[('privateKeyHeader',rb'-----BEGIN (?:OPENSSH |RSA |EC |)PRIVATE KEY-----'),('githubToken',rb'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b'),('awsAccessKey',rb'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b')]
exact=[];candidates=[];count=0
for p in DEST.rglob('*'):
 if not p.is_file():continue
 count+=1;b=read(p)
 for label,secret in protected:
  if secret and secret in b:exact.append({'path':p.relative_to(DEST).as_posix(),'kind':label})
 for label,pattern in generic:
  if re.search(pattern,b):candidates.append({'path':p.relative_to(DEST).as_posix(),'kind':label})
save('secret-screen.json',{'filesScanned':count,'protectedValueCandidatesCompared':len(protected),'exactProtectedInputMatches':exact,'genericPatternCandidates':candidates,'limits':'Heuristic and exact protected input comparison, not proof of absence of every secret. No protected candidate values are written.'})
assert not exact
print(json.dumps({'rawArtifacts':len(raw),'frozenPreviousFiles':len(old_seal['files']),'sourceFiles':len(files),'sourceErrors':len(source_errors),'secretExactMatches':len(exact),'genericCandidates':candidates}),flush=True)
