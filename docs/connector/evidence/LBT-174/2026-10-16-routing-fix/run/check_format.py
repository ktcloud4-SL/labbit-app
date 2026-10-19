from pathlib import Path
import subprocess as sp,json,hashlib
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
manifest=json.loads((OUT/'source-manifest.json').read_text(encoding='utf8'))
files=[p for p in manifest['files'] if p.endswith('.go')]
bad=[]
for name in files:
 raw=(ROOT/name).read_bytes().replace(b'\r\n',b'\n')
 p=sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe'],input=raw,stdout=sp.PIPE,stderr=sp.PIPE,check=True)
 if p.stdout!=raw:bad.append(name)
mismatch=[p for p,v in manifest['files'].items() if hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest()!=v]
result={'checkedGoFiles':len(files),'formatMismatches':bad,'manifestMismatches':mismatch,'sourceSHA256':manifest['sourceSHA256'],'normalization':'canonical LF, consistent with source archive and Git CI'}
(OUT/'gofmt-result.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf8');print(json.dumps(result),flush=True)
assert not bad and not mismatch
