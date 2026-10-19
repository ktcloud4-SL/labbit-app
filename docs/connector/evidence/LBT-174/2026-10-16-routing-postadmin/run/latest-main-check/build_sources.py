from pathlib import Path
import subprocess as sp,json,hashlib,urllib.request,tarfile,io,os
OUT=Path(__file__).resolve().parent
ROOT=Path('C:\\Users\\user\\Documents\\ChatGPT\\3차 2\\tmp\\lbt174-routing-postadmin-20261016\\latest-main-integration')
GO=Path(r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe')
def save(name,v):(OUT/name).write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
def git(*a):return sp.check_output(['git',*a],cwd=ROOT).decode().splitlines()
paths=set(git('ls-files','--','*.go','go.mod','go.sum','runtime/*','contracts/*'))|set(git('ls-files','--others','--exclude-standard','--','*.go'))
files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in sorted(paths)}
source={'base':git('rev-parse','origin/main')[0],'head':git('rev-parse','HEAD')[0],'branch':git('branch','--show-current')[0],'files':files,'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()}
save('source-manifest.json',source)
mode=__import__('sys').argv[1]
if mode=='toolchain':
 url='https://go.dev/dl/?mode=json&include=all'
 metadata=json.load(urllib.request.urlopen(url,timeout=60))
 selected=next(f for v in metadata if v['version']=='go1.27.1' for f in v['files'] if f['filename']=='go1.27.1.linux-amd64.tar.gz')
 target=OUT/selected['filename']
 if not target.exists():
  with urllib.request.urlopen('https://go.dev/dl/'+selected['filename'],timeout=90) as r,target.open('wb') as w:
   import shutil;shutil.copyfileobj(r,w)
 digest=hashlib.sha256(target.read_bytes()).hexdigest();assert digest==selected['sha256']
 save('linux-go-download.json',{'officialMetadataURL':url,'file':selected,'actualSHA256':digest,'actualBytes':target.stat().st_size})
 print(json.dumps({'toolchain':selected['filename'],'sha256':digest,'bytes':target.stat().st_size}),flush=True)
elif mode=='source':
 vendor=OUT/'vendor';assert vendor.resolve().is_relative_to(OUT.resolve())
 with (OUT/'vendor.log').open('wb') as log:sp.run([str(GO),'mod','vendor','-o',str(vendor)],cwd=ROOT,stdout=log,stderr=sp.STDOUT,check=True)
 current={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in source['files']};assert current==files
 tracked=set(git('ls-files'))|paths
 with tarfile.open(OUT/'linux-source.tar.gz','w:gz') as tar:
  for p in sorted(tracked):
   path=ROOT/p
   if not path.is_file():continue
   b=path.read_bytes()
   if p in files:b=b.replace(b'\r\n',b'\n')
   info=tarfile.TarInfo('source/'+p);info.size=len(b);info.mode=0o644;tar.addfile(info,io.BytesIO(b))
  b=json.dumps(source,sort_keys=True).encode();info=tarfile.TarInfo('source/tested-source.json');info.size=len(b);tar.addfile(info,io.BytesIO(b))
  tar.add(vendor,arcname='source/vendor')
 archive=OUT/'linux-source.tar.gz'
 save('linux-source-archive.json',{'sourceSHA256':source['sourceSHA256'],'sourceFiles':len(files),'trackedAndGoPaths':len(tracked),'vendoredFiles':sum(p.is_file() for p in vendor.rglob('*')),'archiveSHA256':hashlib.sha256(archive.read_bytes()).hexdigest(),'archiveBytes':archive.stat().st_size,'protectedInputsIncluded':False})
 print((OUT/'linux-source-archive.json').read_text(),flush=True)
