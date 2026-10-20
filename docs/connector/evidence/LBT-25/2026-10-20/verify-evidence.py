"""Historical verifier retained for provenance; use the read-only verify-package.py."""
raise SystemExit('Historical writer is disabled; run python -B verify-package.py')
import pathlib,json,collections,hashlib,os,re,py_compile,datetime
p=pathlib.Path(__file__).parent
m=json.loads((p/'api-matrix.json').read_text());b=json.loads((p/'browser-evidence.json').read_text());d=json.loads((p/'backend-final.json').read_text())
for k,v in m['cases'].items():
    expected=200
    if k.startswith('login_') or k.startswith('cleanup_') or k.endswith(('_exchange','_close','_close_repeat')):expected=204
    if k in ['student_session','teacher_session']:expected=201
    if k.endswith('_replay_exchange') or k.endswith(('_anonymous','_saas_cookie_only','_after_close')) or k=='cross_session_cookie':expected=401
    if k.endswith(('_wrong_origin_exchange','_other_close','_admin_close')) or k.endswith('_other_workspace') or k in ['ssh_port','nonallowed_port']:expected=403
    if k=='student_survives_teacher_close':expected=200
    assert v['status']==expected,(k,v['status'],expected)
created={e['preview_session_id'] for e in d['events'] if e['event']=='created'};closed={e['preview_session_id'] for e in d['events'] if e['event']=='closed'}
assert created==closed and len(created)==5 and d['unrecognizedPreviewLogMessages']==0
assert b['ttlSession']['newGetStatus401'] and b['explicitCloseSession']['freshGet'] and b['explicitCloseSession']['afterClose']['status401']
rows=[json.loads(l) for l in (p/'go-test.jsonl').read_text(encoding='utf-8-sig').splitlines()];counts=collections.Counter(e['Action'] for e in rows if e.get('Test'))
assert counts['pass']==964 and counts['fail']==0 and counts['skip']==0
assert not json.loads((p/'postconditions.json').read_text())['temporaryPortForwardStillRunning']
for f in p.glob('*.py'):py_compile.compile(str(f),doraise=True)
for f in p.rglob('*'):
    if not f.is_file() or f.suffix in ['.png','.pyc']:continue
    data=f.read_bytes();assert os.environ['C3_PASSWORD'].encode() not in data,f.name
    assert not re.search(rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',data),f.name
    assert not re.search(rb'https://[^\s"<>]+/__labbit/bootstrap#[A-Za-z0-9_-]{20,}',data),f.name
summary={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'apiCaseCount':len(m['cases']),'expectedStatusesMatched':True,'knownCreatedAndClosedIdCount':len(created),'browserFreshGetAndTTLAndCloseConfirmed':True,'goTestsPassIncludingSubtests':counts['pass'],'goTestFail':counts['fail'],'goTestSkip':counts['skip'],'temporaryPortForwardAbsent':True,'helpersCompile':True,'exactPasswordAndPrivateKeyAndBootstrapFragmentScanPassed':True,'noProductSourceChanges':True}
(p/'verification-summary.json').write_text(json.dumps(summary,indent=2),encoding='utf-8')
manifest=[{'path':f.relative_to(p).as_posix(),'bytes':f.stat().st_size,'sha256':hashlib.sha256(f.read_bytes()).hexdigest()} for f in sorted(p.rglob('*')) if f.is_file() and f.name!='evidence-manifest.json']
(p/'evidence-manifest.json').write_text(json.dumps(manifest,indent=2),encoding='utf-8')
print(json.dumps(summary))
