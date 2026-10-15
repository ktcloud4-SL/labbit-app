import hashlib, json, os, subprocess
from pathlib import Path
repo = Path(os.environ['LABBIT_EVIDENCE_REPO'])
out = Path(os.environ['LABBIT_EVIDENCE_OUT'])
out.mkdir(parents=True, exist_ok=True)
def sha(b): return hashlib.sha256(b).hexdigest()
old = json.loads((repo/'docs/connector/evidence/LBT-151/2026-10-14/binary-manifest.json').read_text(encoding='utf-8'))
current = json.loads((out.parent/'build/binary-manifest.json').read_text(encoding='utf-8'))
paths = subprocess.check_output(['git','diff','--name-only','7917531','HEAD','--','tools'],cwd=repo,text=True).splitlines()
mapping, files = {}, []
for n in paths:
    if not n.endswith('.go'): continue
    raw = (repo/n).read_bytes()
    lf = raw.replace(b'\r\n', b'\n')
    target = out/'lf-overlay'/n
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(lf)
    mapping[str(repo/n)] = str(target)
    files.append({'path':n,'rawSHA256':sha(raw),'lfSHA256':sha(lf),'crlfPairs':raw.count(b'\r\n')})
overlay = out/'overlay.json'
overlay.write_text(json.dumps({'Replace':mapping}),encoding='utf-8')
normalized = {}
for name, target, goos in [('lbt145-control-linux','./tools/ktcloud-d1-acceptance','linux'),('bootstrap.exe','./tools/ktcloud-op02-bootstrap','windows')]:
    cmd=['go','build','-mod=readonly','-buildvcs=false','-overlay',str(overlay),'-o',str(out/name),target]
    env=dict(os.environ,GOOS=goos,GOARCH='amd64',CGO_ENABLED='0')
    result=subprocess.run(cmd,cwd=repo,env=env,capture_output=True)
    (out/(name+'.build.stdout')).write_bytes(result.stdout)
    (out/(name+'.build.stderr')).write_bytes(result.stderr)
    if result.returncode: raise SystemExit(result.returncode)
    normalized[name]=sha((out/name).read_bytes())
comparison={n:{'actualOct14':h,'freshOct15':current['sha256'][n],'rawEqual':h==current['sha256'][n],**({'lfOverlayOct15':normalized[n],'overlayEqual':h==normalized[n]} if n in normalized else {})} for n,h in old['sha256'].items()}
report={'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip(),'comparison':comparison,'overlaySourceFiles':files,'note':'Overlay changes only CRLF to LF in the 21 LBT151 changed tool Go files. Tracked source is untouched. Binary byte identity is required before attributing original fixture binaries to current source; production binaries use fresh ordinary build.'}
(out/'binary-comparison.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8')
print(json.dumps(comparison,indent=2))
if not all(c['rawEqual'] or c.get('overlayEqual') for c in comparison.values()): raise SystemExit('Unexplained binary difference')
