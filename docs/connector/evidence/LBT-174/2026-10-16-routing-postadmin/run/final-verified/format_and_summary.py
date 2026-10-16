from pathlib import Path
import subprocess as sp,json,hashlib,time
OUT=Path(__file__).resolve().parent
ROOT=Path(r"C:\Users\user\Documents\ChatGPT\3차 2\tmp\labbit-connector-gates-20261015")
manifest=json.loads((OUT/'source-manifest.json').read_text(encoding='utf8'));bad=[];start=time.monotonic()
fmt=r"C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe"
for p in manifest['files']:
 if not p.endswith('.go'):continue
 b=(ROOT/p).read_bytes().replace(b'\r\n',b'\n');r=sp.run([fmt],input=b,stdout=sp.PIPE,stderr=sp.PIPE)
 if r.returncode or r.stdout!=b:bad.append(p)
result={'normalization':'canonical LF','goFiles':sum(p.endswith('.go') for p in manifest['files']),'bad':bad,'seconds':round(time.monotonic()-start,3),'sourceSHA256':manifest['sourceSHA256']}
(OUT/'gofmt-result.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf8');print(result);assert not bad
for name in ['go-test','go-race']:
 events=[json.loads(line) for line in (OUT/(name+'.log')).read_text(encoding='utf8').splitlines() if line.startswith('{')]
 ended=[e for e in events if e.get('Action') in ('pass','fail','skip')]
 result={'testPass':sum(e['Action']=='pass' and 'Test' in e for e in ended),'testFail':[e for e in ended if e['Action']=='fail' and 'Test' in e],'testSkip':[e for e in ended if e['Action']=='skip' and 'Test' in e],'packagePass':sum(e['Action']=='pass' and 'Test' not in e for e in ended),'packageFail':[e for e in ended if e['Action']=='fail' and 'Test' not in e]}
 (OUT/(name+'-summary.json')).write_text(json.dumps(result,indent=2)+'\n',encoding='utf8');print(name,{k:(len(v) if isinstance(v,list) else v) for k,v in result.items()})
