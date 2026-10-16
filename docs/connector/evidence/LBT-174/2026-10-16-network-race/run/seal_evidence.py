from pathlib import Path
import json,hashlib,re,urllib.parse,datetime
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-network-race'
missing=[]
for p in DEST.rglob('*.md'):
 for target in re.findall(r'\[[^\]]+\]\(([^)\n]+)\)',p.read_text(encoding='utf8')):
  target=target.strip('<>')
  if '://' in target or target.startswith('#'):continue
  target=urllib.parse.unquote(target.split('#')[0])
  if target and not (p.parent/target).exists():missing.append({'file':p.relative_to(DEST).as_posix(),'target':target})
assert not missing,missing
paths=[p for p in DEST.rglob('*') if p.is_file() and p.name!='package-checksums.json']
seal={'recordedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'files':{p.relative_to(DEST).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}}
(DEST/'package-checksums.json').write_text(json.dumps(seal,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
recheck=json.loads((DEST/'package-checksums.json').read_text(encoding='utf8'))
assert all(hashlib.sha256((DEST/p).read_bytes()).hexdigest()==v for p,v in recheck['files'].items())
print(json.dumps({'sealedFiles':len(paths),'checksumMismatches':0,'brokenMarkdownLinks':0,'sourceGoFilesInEvidence':len(list(DEST.rglob('*.go')))}),flush=True)
