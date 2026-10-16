from pathlib import Path
import hashlib,json,gzip,re,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-routing-fix'
DEST.mkdir(parents=True,exist_ok=True)
provenance=[]
for source,prefix in [(OUT,'run'),(OUT.parent/'lbt174-routing-fix-independent-20261016','independent-review')]:
 for p in sorted(source.rglob('*')):
  if not p.is_file() or any(x in p.parts for x in ['vendor','__pycache__','latest-main-document-check']):continue
  if p.suffix not in ['.py','.ps1','.go','.sh','.json','.log','.md','.txt']:continue
  if p.name.startswith('vmnet8-dhcp-admin-state'):continue
  relative=p.relative_to(source);dest=DEST/prefix/relative;raw=p.read_bytes();artifact=raw
  if p.suffix=='.go':dest=dest.with_suffix('.go.txt')
  if p.suffix=='.log':dest=dest.with_suffix('.log.gz')
  dest.parent.mkdir(parents=True,exist_ok=True)
  if p.suffix=='.log':
   with dest.open('wb') as f:
    with gzip.GzipFile(filename='',mode='wb',fileobj=f,mtime=0) as gz:gz.write(raw)
  else:
   if p.suffix=='.md':
    def links(match):
     target=match.group(2)
     if '://' in target or target.startswith('#'):return match.group(0)
     base,sep,anchor=target.partition('#')
     if base.endswith('.go'):base+='.txt'
     elif base.endswith('.log'):base+='.gz'
     return match.group(1)+base+(sep+anchor if sep else '')+')'
    artifact=re.sub(r'(\[[^\]]+\]\()([^\)\n]+)\)',links,raw.decode('utf8')).encode('utf8')
   dest.write_bytes(artifact)
  provenance.append({'original':p.relative_to(OUT.parent).as_posix(),'artifact':dest.relative_to(DEST).as_posix(),'rawBytes':len(raw),'rawSHA256':hashlib.sha256(raw).hexdigest(),'artifactUncompressedBytes':len(artifact),'artifactUncompressedSHA256':hashlib.sha256(artifact).hexdigest(),'compression':'gzip' if p.suffix=='.log' else None,'transformation':'Markdown Go/log links use exported suffixes' if artifact!=raw else None})
def git(*args):return sp.check_output(['git',*args],cwd=ROOT)
tracked=git('diff','--name-only','HEAD','--','internal','runtime','contracts').decode().splitlines()
untracked=git('ls-files','--others','--exclude-standard','--','internal','runtime','contracts').decode().splitlines()
inventory=[]
for rel in sorted(set(tracked+untracked)):
 p=ROOT/rel;raw=p.read_bytes();dest=DEST/'changed-source'/rel
 if p.suffix=='.go':dest=dest.with_suffix('.go.txt')
 dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(raw)
 inventory.append({'path':rel,'state':'untracked' if rel in untracked else 'tracked-diff','rawSHA256':hashlib.sha256(raw).hexdigest(),'artifact':dest.relative_to(DEST).as_posix()})
(DEST/'tracked-diff.patch').write_bytes(git('diff','HEAD','--','internal','runtime','contracts'))
(DEST/'working-tree.txt').write_bytes(git('status','--short'))
context=[]
for rel in ['runtime/README.md','db/migrations/README.md']:
 p=ROOT/rel;raw=p.read_bytes();dest=DEST/'changed-source'/rel
 dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(raw)
 context.append({'path':rel,'role':'unchanged linked contract context; not one of the 37 changed files','rawSHA256':hashlib.sha256(raw).hexdigest(),'artifact':dest.relative_to(DEST).as_posix()})
for name,value in [('raw-provenance.json',provenance),('code-inventory.json',inventory)]:
 (DEST/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
(DEST/'context-inventory.json').write_text(json.dumps(context,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
print(json.dumps({'rawArtifacts':len(provenance),'changedFiles':len(inventory),'sourceGoFilesInEvidence':len(list(DEST.rglob('*.go')))}),flush=True)
