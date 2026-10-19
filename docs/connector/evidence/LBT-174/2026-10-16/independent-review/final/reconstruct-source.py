from pathlib import Path
import hashlib, io, json, subprocess, tarfile

HERE=Path(__file__).resolve().parent
REPO=HERE.parent/'labbit-connector-gates-20261015'
E=REPO/'docs/connector/evidence/LBT-174/2026-10-16'
TARGET=HERE/'reconstruction-isolated'
TARGET.mkdir(exist_ok=False)
manifest=json.loads((E/'source-manifest.json').read_text(encoding='utf-8-sig'))
archive=subprocess.check_output(['git','archive',manifest['head']],cwd=REPO)
with tarfile.open(fileobj=io.BytesIO(archive)) as tf:
    tf.extractall(TARGET,filter='data')
# The user's enclosing workspace is itself a Git repository. Isolate this
# exported copy so git apply does not silently skip all paths as out-of-prefix.
subprocess.run(['git','init','-q',str(TARGET)],check=True)
r=subprocess.run(['git','apply',str(E/'source-diff.patch')],cwd=TARGET,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
differences=[]
if r.returncode==0:
    for path,expected in manifest['files'].items():
        file=TARGET/path
        actual=hashlib.sha256(file.read_bytes().replace(b'\r\n',b'\n')).hexdigest() if file.exists() else None
        if actual!=expected: differences.append({'path':path,'actual':actual,'expected':expected})
result={'base':manifest['head'],'archiveSHA256':hashlib.sha256(archive).hexdigest(),'patchSHA256':hashlib.sha256((E/'source-diff.patch').read_bytes()).hexdigest(),'gitApplyExit':r.returncode,'gitApplyOutput':(r.stdout+r.stderr).decode('utf-8',errors='replace'),'comparedFiles':len(manifest['files']) if r.returncode==0 else 0,'differences':differences}
(HERE/'source-reconstruction-check-isolated.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(result,ensure_ascii=False,indent=2))
raise SystemExit(0 if r.returncode==0 and not differences else 1)
