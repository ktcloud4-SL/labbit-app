from pathlib import Path
import gzip, hashlib, json, re, subprocess

HERE=Path(__file__).resolve().parent
REPO=HERE.parent/'labbit-connector-gates-20261015'
E=REPO/'docs/connector/evidence/LBT-174/2026-10-16'
RAW=HERE.parent/'lbt174-final-audit-20261016'
errors=[]
def digest(b): return hashlib.sha256(b).hexdigest()
inventory={p.relative_to(E).as_posix():{'bytes':p.stat().st_size,'sha256':digest(p.read_bytes())} for p in sorted(E.rglob('*')) if p.is_file()}
for name in inventory:
    if name.endswith('.json'):
        try: json.loads((E/name).read_text(encoding='utf-8-sig'))
        except Exception as exc: errors.append({'file':name,'json':str(exc)})
source=json.loads((E/'source-manifest.json').read_text(encoding='utf-8-sig'))
source_differences=[p for p,h in source['files'].items() if digest((REPO/p).read_bytes().replace(b'\r\n',b'\n'))!=h]
if source_differences: errors.append({'source_differences':source_differences})
aggregate=digest(json.dumps(source['files'],sort_keys=True).encode())
if aggregate!=source['sourceSHA256']: errors.append({'source_aggregate':aggregate})
copies=[]
for p in sorted(RAW.glob('*')):
    target=E/p.name
    if p.is_file() and target.is_file():
        equal=p.read_bytes()==target.read_bytes()
        copies.append({'file':p.name,'rawCopyEqual':equal})
        if not equal: errors.append({'copy':p.name})
gzip_checks=[]
for p in sorted(E.rglob('*.gz')):
    decompressed=gzip.decompress(p.read_bytes())
    record={'file':p.relative_to(E).as_posix(),'decompressedBytes':len(decompressed),'decompressedSHA256':digest(decompressed)}
    events=[json.loads(line) for line in decompressed.decode('utf-8').splitlines() if line.startswith('{')]
    record['testPass']=sum(x.get('Action')=='pass' and 'Test' in x for x in events)
    record['testFail']=sum(x.get('Action')=='fail' and 'Test' in x for x in events)
    record['testSkip']=sum(x.get('Action')=='skip' and 'Test' in x for x in events)
    record['packagePass']=sum(x.get('Action')=='pass' and 'Test' not in x for x in events)
    original=None
    if p.name=='go-test-serial.jsonl.gz' and p.parent==E: original=RAW/'go-test-serial.log'
    if original and original.exists():
        record['originalEqual']=original.read_bytes()==decompressed
        if not record['originalEqual']: errors.append({'gzip':record['file']})
    gzip_checks.append(record)
patch=(E/'source-diff.patch').read_text(encoding='utf-8-sig')
patch_paths=re.findall(r'^diff --git a/(.+?) b/(.+)$',patch,re.M)
result={'sourceFiles':len(source['files']),'sourceSHA256':aggregate,'sourceDifferences':source_differences,'files':inventory,'jsonFilesParsed':sum(x.endswith('.json') for x in inventory),'rawCopies':copies,'gzipChecks':gzip_checks,'sourcePatchFiles':[b for a,b in patch_paths],'errors':errors}
(HERE/'evidence-packaging-check.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps({k:v for k,v in result.items() if k not in ['files','rawCopies']},ensure_ascii=False,indent=2))
raise SystemExit(1 if errors else 0)
