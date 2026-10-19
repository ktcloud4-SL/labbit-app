from pathlib import Path
import hashlib,json,gzip,shutil,re
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-network-race'
DEST.mkdir(parents=True,exist_ok=True)
provenance=[]
def copytree(source,prefix):
 for p in sorted(source.rglob('*')):
  if not p.is_file() or 'vendor' in p.parts or '__pycache__' in p.parts:continue
  if p.suffix not in ['.py','.go','.sh','.json','.log','.md','.txt']:continue
  if p.name in ['package_evidence.py','verify_evidence.py']:pass
  relative=p.relative_to(source);dest=DEST/prefix/relative
  raw=p.read_bytes();data=raw;transformation=None
  if source==OUT and relative.as_posix()=='linux-apt.log':
   data=data.replace(b'198.24.0.1',b'<lab-gateway>')
   transformation='Diagnostic VM Lab gateway in helper traceback replaced by role marker. Original retained outside Git; original hash/length preserved.'
  if p.suffix=='.md':
   value=data.decode('utf8')
   value=re.sub(r'(\]\([^):\n]+\.go)(\))',r'\1.txt\2',value)
   value=re.sub(r'(\]\([^):\n]+\.log)(\))',r'\1.gz\2',value)
   if value.encode('utf8')!=data:
    data=value.encode('utf8');transformation='Markdown relative links adjusted for .go.txt source aliases and .log.gz compressed artifacts; original hash/length preserved.'
  if p.suffix=='.go':dest=dest.with_suffix('.go.txt')
  if p.suffix=='.log':dest=dest.with_suffix('.log.gz')
  dest.parent.mkdir(parents=True,exist_ok=True)
  if p.suffix=='.log':
   with dest.open('wb') as stream:
    with gzip.GzipFile(filename='',mode='wb',fileobj=stream,mtime=0) as gz:gz.write(data)
  else:dest.write_bytes(data)
  provenance.append({'original':str(p.relative_to(OUT.parent)).replace('\\','/'),'artifact':dest.relative_to(DEST).as_posix(),'rawBytes':len(raw),'rawSHA256':hashlib.sha256(raw).hexdigest(),'artifactUncompressedBytes':len(data),'artifactUncompressedSHA256':hashlib.sha256(data).hexdigest(),'transformation':transformation,'compression':'gzip' if p.suffix=='.log' else None})
copytree(OUT,'run')
copytree(OUT.parent/'lbt174-network-race-independent-20261016','independent-review')
(DEST/'raw-provenance.json').write_text(json.dumps(provenance,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
print(json.dumps({'rawArtifacts':len(provenance),'sourceGoFilesInEvidence':len(list(DEST.rglob('*.go')))}),flush=True)
