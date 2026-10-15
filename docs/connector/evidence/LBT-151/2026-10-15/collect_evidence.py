"""Publish only safe validation text/metadata from a fresh Oct15 output root.

LABBIT_EVIDENCE_REPO identifies this checkout. LABBIT_EVIDENCE_RUN_ROOT
contains auto, web, contracts, build, latest-main and binary-comparison.
Does not read cloud credentials, private keys, provider responses or VM data.
"""
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import zipfile

repo = Path(os.environ['LABBIT_EVIDENCE_REPO']).resolve(strict=True)
root = Path(os.environ['LABBIT_EVIDENCE_RUN_ROOT']).resolve(strict=True)
dest = Path(__file__).resolve().parent
def sha(b): return hashlib.sha256(b).hexdigest()
def save(n, value): (dest/n).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
def load(p): return json.loads(p.read_text(encoding='utf-8'))
def git(*args): return subprocess.check_output(['git',*args],cwd=repo,text=True).strip()
def lf(p): return p.read_bytes().replace(b'\r\n',b'\n')
head, base = git('rev-parse','HEAD'),git('rev-parse','origin/main')
assert git('merge-base','HEAD','origin/main') == base
old = {x['path']:x['sha256LF'] for x in load(repo/'docs/connector/evidence/LBT-151/2026-10-14/source-manifest.json')['files']}
new = load(root/'auto/compiled-source-auto-after.json')
new_files = {x['path']:x['sha256LF'] for x in new['files']}
assert new['head']==head and all(sha(lf(repo/n))==h for n,h in new_files.items())
modified = sorted(n for n in old.keys() & new_files.keys() if old[n]!=new_files[n])
added = sorted(new_files.keys()-old.keys())
removed = sorted(old.keys()-new_files.keys())
main_paths = git('diff','--name-only','7917531',base).splitlines()
assert not removed and all(n in main_paths for n in modified+added)
tool_paths = git('diff','--name-only',base,'HEAD','--','tools').splitlines()
assert not git('diff','8971ad5','HEAD','--','tools','docs/connector/evidence/LBT-151/2026-10-14')
assert all(new_files[n]==old[n] for n in tool_paths if n.endswith('.go'))
save('integration-impact.json',{'recordedAt':datetime.now(timezone.utc).isoformat(),'testedHead':head,'originMain':base,'previousLocalHead':'8971ad595902391d85c8b2fb0c50145e32455557','oldBase':'7917531fe630357eeb1f055ea2ad08dbee93a00e','method':'git rebase origin/main; git range-diff confirms unchanged original LBT151 patch','mainChangedPaths':main_paths,'compiledSourceCounts':{'oct14':len(old),'oct15':len(new_files)},'compiledSourceModified':modified,'compiledSourceAdded':added,'compiledSourceRemoved':removed,'originalToolsAndOct14EvidenceGitDiffEmpty':True,'lbt151GoLFIdenticalToActualOct14':True,'noCloudExecutionOct15':True,'sourceIdentityScope':'Go/go.mod/go.sum LF list; web/contracts are identified by tested Git HEAD, unchanged tracked code and exact upstream main tree.'})
save('source-manifest.json',new)
save('binary-comparison.json',load(root/'binary-comparison/binary-comparison.json'))
save('latest-ssot.json',load(root/'latest-ssot.json'))
save('not-executed.json',load(root/'latest-main/not-executed.json'))
summary={}
for mode in ['auto','web','contracts','build','latest-main']:
    summary[mode]={}
    for p in sorted((root/mode).glob('*-run.json')):
        value=load(p)
        assert value.get('exitCode')==0, p
        summary[mode][p.stem]=value
    for p in sorted((root/mode).glob('*-source-stability.json')):
        value=load(p)
        assert value.get('compiledSourceUnchanged',value.get('unchanged')) is True,p
        summary[mode][p.stem]=value
web=(root/'web/web-test.log').read_text(encoding='utf-8')
web=re.sub(r'\x1b\[[0-9;]*m','',web)
match=re.search(r'Test Files\s+(\d+) passed.*?Tests\s+(\d+) passed',web,re.S)
assert match
summary['web']['vitest']={'filesPass':int(match[1]),'testsPass':int(match[2])}
summary['web']['lock']=load(root/'web/web-lock-check.json')
assert summary['web']['lock']['unchanged']
save('validation-summary.json',summary)
outputs={}; skips=[]
for line in (root/'auto/go-unit.log').read_text(encoding='utf-8').splitlines():
    try: item=json.loads(line)
    except ValueError: continue
    key=(item.get('Package'),item.get('Test'))
    if item.get('Action')=='output' and item.get('Test'): outputs.setdefault(key,[]).append(item.get('Output',''))
    if item.get('Action')=='skip' and item.get('Test'):
        skips.append({'package':key[0],'test':key[1],'output':''.join(outputs.get(key,[])),'time':item.get('Time')})
save('default-suite-skips.json',{'count':len(skips),'items':skips,'notIncluded':'Linux tagged/PostgreSQL suite not invoked; see not-executed.json. Cloud proof is a separate historical opt-in execution.'})
manifest=[]; published=[]
ip=re.compile(rb'(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?![\w.])')
with zipfile.ZipFile(dest/'validation-logs.zip','w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as archive:
    for mode in ['auto','web','contracts','build','latest-main','binary-comparison']:
        for p in sorted((root/mode).iterdir()):
            if not p.is_file() or not (p.suffix in ['.json','.log','.txt','.stdout','.stderr']): continue
            if p.name=='overlay.json': continue
            raw=p.read_bytes()
            data,count=ip.subn(b'<IP_REDACTED>',raw)
            name=mode+'/'+p.name
            archive.writestr(name,data)
            published.append((name,data))
            manifest.append({'path':name,'originalSHA256':sha(raw),'originalBytes':len(raw),'publishedSHA256':sha(data),'publishedBytes':len(data),'ipv4Redactions':count})
save('raw-log-manifest.json',{'entries':manifest,'zipSHA256':sha((dest/'validation-logs.zip').read_bytes()),'policy':'Raw text/metadata only; originals SHA recorded; IPv4 replaced for publication. Binaries/overlay source/credentials excluded.'})
patterns=[rb'-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----',rb'(?i)x-auth-token\s*[:=]\s*[^\s"<>]{12,}',rb'(?i)(?:password|credential|secret)\s*[:=]\s*"[^"<>]{24,}"']
hits=[{'path':n,'pattern':i} for n,b in published for i,p in enumerate(patterns) if re.search(p,b)]
save('publication-scan.json',{'entries':len(manifest),'ipv4Redactions':sum(m['ipv4Redactions'] for m in manifest),'restrictedPatternFindings':hits,'limit':'Restricted pattern scan and manual metadata review, not comprehensive DLP. Only local unit/Mock/static build logs from this run are eligible.'})
assert not hits,hits
print(json.dumps({'head':head,'base':base,'goSourceFiles':len(new_files),'modified':modified,'added':added,'defaultSkips':len(skips),'vitest':summary['web']['vitest'],'zipEntries':len(manifest),'zipSHA256':sha((dest/'validation-logs.zip').read_bytes())},indent=2))
