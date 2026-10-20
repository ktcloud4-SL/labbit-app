"""Offline, read-only verification; no authentication, helper execution or file writes."""
import ast,collections,hashlib,json,pathlib,re
root=pathlib.Path(__file__).resolve().parent

def canonical(path):
    data=path.read_bytes()
    return data if path.suffix=='.png' else data.replace(b'\r\n',b'\n')

def read(name):
    return json.loads((root/name).read_text(encoding='utf-8-sig'))

manifest=read('package-manifest.json')
expected={r['path'] for r in manifest}
actual={p.relative_to(root).as_posix() for p in root.rglob('*') if p.is_file() and p.name!='package-manifest.json' and '__pycache__' not in p.parts}
assert expected==actual, 'manifest file-set mismatch'
for row in manifest:
    path=root/row['path']
    assert root in path.resolve().parents and not path.is_symlink()
    data=canonical(path)
    assert len(data)==row['canonicalLFBytes'] and hashlib.sha256(data).hexdigest()==row['canonicalLFSHA256'],row['path']
m=read('api-matrix.json');b=read('browser-evidence.json');d=read('backend-final.json')
assert len(m['cases'])==39
expected_status={
    **{f'login_{role}':204 for role in ('student','instructor','admin')},
    **{f'{role}_other_workspace':403 for role in ('student','instructor','admin')},
    'ssh_port':403,'nonallowed_port':403,'cross_session_cookie':401,
    'first_session_still_active':200,'student_survives_teacher_close':200,
    'cleanup_student':204,'cleanup_instructor':204,
}
for role in ('student','teacher'):
    prefix=f'{role}_session'
    expected_status[prefix]=201
    for suffix,status in {'anonymous':401,'saas_cookie_only':401,'wrong_origin_exchange':403,
                         'exchange':204,'replay_exchange':401,'get':200,'other_close':403,
                         'admin_close':403,'still_active':200,'close':204,'after_close':401,
                         'close_repeat':204}.items():
        expected_status[f'{prefix}_{suffix}']=status
assert set(expected_status)==set(m['cases'])
for key,status in expected_status.items():
    value=m['cases'][key]
    assert value['status']==status,(key,value['status'],status)
for key in ('student_session','teacher_session'):
    assert all(m[key][flag] for flag in ('separateOriginSameSite','noStore','cookieSecureHostOnlyHttpOnly','appGetMarker','unauthorizedCloseDidNotInvalidate'))
created={e['preview_session_id'] for e in d['events'] if e['event']=='created'}
closed={e['preview_session_id'] for e in d['events'] if e['event']=='closed'}
assert created==closed and len(created)==5 and d['unrecognizedPreviewLogMessages']==0
assert b['ttlSession']['newGetStatus401'] and b['explicitCloseSession']['freshGet'] and b['explicitCloseSession']['afterClose']['status401']
assert read('api-app-not-running.json')['create']['status']==502 and read('api-app-not-running.json')['create']['code']=='preview_app_not_running'
counts=collections.Counter();packages=0
for line in (root/'go-test.jsonl').read_text(encoding='utf-8-sig').splitlines():
    event=json.loads(line)
    if event.get('Test'):counts[event['Action']]+=1
    elif event['Action']=='pass':packages+=1
assert counts['pass']==964 and counts['fail']==counts['skip']==0 and packages==7
assert not read('postconditions.json')['temporaryPortForwardStillRunning']
for path in root.glob('*.py'):ast.parse(path.read_text(encoding='utf-8'),filename=path.name)
for path in root.rglob('*'):
    if not path.is_file() or path.suffix=='.png' or '__pycache__' in path.parts:continue
    data=path.read_bytes()
    assert not re.search(rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',data),path.name
    assert not re.search(rb'https://[^\s"<>]+/__labbit/bootstrap#[A-Za-z0-9_-]{20,}',data),path.name
print(json.dumps({'packageHashesAndFileSetMatched':True,'apiExpectedStatuses':39,'knownCreatedEqualsClosed':5,'historicalGoTestPassIncludingSubtests':counts['pass'],'historicalGoPackages':packages,'goFail':0,'goSkip':0,'readOnly':True,'helpersASTParse':True,'privateKeyAndBootstrapFragmentScanPassed':True}))
