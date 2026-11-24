import hashlib,json,pathlib,subprocess,sys
r=pathlib.Path(sys.argv[1]); mode=sys.argv[2]
o=r/'evidence/LBT-60/2026-11-23-pr196-publication'
def blob(b):return hashlib.sha1(b'blob '+str(len(b)).encode()+b'\0'+b).hexdigest()
def manifest():
 files=[]
 for p in sorted(o.rglob('*')):
  if p.is_file() and p.name!='artifact-manifest.json':
   b=p.read_bytes();files.append(dict(path=p.relative_to(o).as_posix(),size=len(b),sha256=hashlib.sha256(b).hexdigest()))
 (o/'artifact-manifest.json').write_text(json.dumps(dict(files=files,scope='directory-relative exact original bytes; self excluded'),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
def check():
 entries={}
 for raw in subprocess.check_output(['git','ls-files','--stage','-z'],cwd=r).split(b'\0'):
  if raw:
   meta,path=raw.split(b'\t');entries[path.decode()]=meta.decode().split()[1]
 source=json.loads((o/'publication-source.json').read_text(encoding='utf-8'))['files']
 for f in source:assert entries[f['path']]==f['expected_git_blob'],f['path']
 evidence=[]
 for p in sorted((r/'evidence').rglob('*')):
  if p.is_file():
   path=p.relative_to(r).as_posix();b=p.read_bytes()
   assert entries[path]==blob(b),path
   evidence.append(dict(path=path,raw_git_blob=blob(b),size=len(b),sha256=hashlib.sha256(b).hexdigest()))
 return source,evidence
if mode=='manifest':manifest()
elif mode=='record':
 source,evidence=check()
 (o/'index-verification.json').write_text(json.dumps(dict(source_count=len(source),raw_evidence_count=len(evidence),source_canonical_lf_matches_index=True,evidence_original_bytes_match_index=True,files=evidence,scope='Index before index-verification.json creation; final all-file check after staging it and regenerating artifact manifest is required'),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
 print(len(source),len(evidence))
elif mode=='final':
 source,evidence=check()
 for f in json.loads((o/'artifact-manifest.json').read_text(encoding='utf-8'))['files']:
  b=(o/f['path']).read_bytes();assert len(b)==f['size'] and hashlib.sha256(b).hexdigest()==f['sha256']
 print(json.dumps(dict(source_count=len(source),raw_evidence_count=len(evidence),index_matches=True,artifact_integrity=True)))
