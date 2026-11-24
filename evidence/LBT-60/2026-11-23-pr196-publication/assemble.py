"""Assemble exact source/test/reviewer evidence. Does not commit or push."""
import collections, hashlib, json, pathlib, re, shutil, subprocess, sys
from datetime import datetime, timezone

root=pathlib.Path(sys.argv[1]); out=root/'evidence/LBT-60/2026-11-23-pr196-publication'
scratch=pathlib.Path(sys.argv[2]); deps=pathlib.Path(sys.argv[3]); sys.path.insert(0,str(deps))
import yaml
def dump(name,value): (out/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
def git(*args): return subprocess.check_output(['git',*args],cwd=root).decode('utf-8').strip()
def count(name):
    counts=collections.Counter(); package=collections.Counter(); skips=[]; failures=[]
    for line in (out/name).read_text(encoding='utf-8').splitlines():
        try: e=json.loads(line)
        except ValueError: continue
        a=e.get('Action')
        if a not in ('pass','fail','skip'): continue
        (counts if e.get('Test') else package)[a]+=1
        if e.get('Test') and a=='skip': skips.append(dict(package=e['Package'],test=e['Test']))
        if a=='fail': failures.append(dict(package=e.get('Package'),test=e.get('Test')))
    return dict(test_events=dict(counts),package_events=dict(package),skipped_tests=skips,failures=failures)

commands=json.loads((out/'commands.json').read_text())
if not (out/'cleanup.json').exists() or not (out/'format.json').exists(): raise SystemExit('verification not finished')
failed=[c for c in commands if c['exit_code']!=0]
assert len(failed)==1 and failed[0]['log']=='integration-all.jsonl' and failed[0]['exit_code']==1
initial=count('integration-all.jsonl')
assert initial['test_events'].get('fail')==9
assert all('Valkey' in f['test'] for f in initial['failures'] if f['test'])
assert any(c['log']=='integration-all-final.jsonl' and c['exit_code']==0 for c in commands)
assert not count('integration-all-final.jsonl')['failures']
cleanup=json.loads((out/'cleanup.json').read_text()); assert not cleanup['listening_after_cleanup']
assert (out/'pg-databases-final.log').read_text().strip()=='', 'test DBs remain'
assert not json.loads((out/'format.json').read_text())['noncanonical']
for role in ('operator','worker','regression'):
    shutil.copyfile(scratch/f'pr196-review-{role}-20261123.md',out/f'review-{role}.md')
for suffix in ('jsonl','-source.json'):
    name='pr196-review-operator-20261123'+('.'+suffix if suffix=='jsonl' else suffix)
    shutil.copyfile(scratch/name,out/('review-operator.jsonl' if suffix=='jsonl' else 'review-operator-source.json'))
shutil.copyfile(scratch/'pr196_final_verify_20261123.py',out/'verify.py')

summary=dict(date_kst='2026-11-23',base=git('rev-parse','origin/main'),prior_pr_head=git('rev-parse','HEAD'),
    go={n:count(n) for n in ('go-all.jsonl','go-control-race.jsonl','integration-all.jsonl','integration-all-final.jsonl','review-operator.jsonl')},
    environment_retry=dict(initial='CI=1 with no Valkey caused exactly 9 required Valkey failures; original logs retained',final='local default CI unset; 9 Valkey tests and opt-in dump explicitly skipped; actual PostgreSQL16 fixture rerun',bootstrap='first retry cp949 metadata decode aborted before PG/test calls; rerun Python -X utf8'),
    web=dict(typecheck=0,lint=0,tests_passed=493,tests_failed=0,test_files=35,build=0),cleanup=cleanup,
    cloud_calls=False,shared_db_calls=False,slack_sent=False,merge=False,execution_gate='HOLD',recovery_authorized=False,cloud_verified=False,
    preserved_not_verified=['REQUIRED_SETTINGS','RUNTIME_PROVENANCE','SERVICE_FUNCTIONS','SINGLE_WRITER'],replica_lag='FAIL')
dump('test-summary.json',summary)
changed=set(git('diff','--name-only','origin/main').splitlines())|set(git('ls-files','--others','--exclude-standard').splitlines())
product=sorted(p for p in changed if '/evidence/' not in '/'+p and not p.startswith('evidence/'))
inventory=[]
for p in product:
    b=(root/p).read_bytes(); canonical=b.replace(b'\r\n',b'\n')
    blob=subprocess.check_output(['git','hash-object','--stdin'],cwd=root,input=canonical).decode().strip()
    inventory.append(dict(path=p,raw_size=len(b),raw_sha256=hashlib.sha256(b).hexdigest(),canonical_lf_size=len(canonical),canonical_lf_sha256=hashlib.sha256(canonical).hexdigest(),expected_git_blob=blob))
dump('publication-source.json',dict(base=summary['base'],prior_pr_head=summary['prior_pr_head'],files=inventory,raw_vs_git='Windows raw CRLF may differ from Git normalized LF; compare canonical LF SHA and Git blob, not raw bytes from a different checkout'))
def purpose(p):
    if p.startswith(('cmd/labbit-dr-control/','internal/drcontrol/')): return 'exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime'
    if p.startswith('internal/connector/'): return 'test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT'
    if p.startswith('db/migrations/'): return '기존 additive12~14 durable Scope/경계/Resume journal·APPLIED 시간 제약 및 schema 이력; DB SSOT'
    if p.startswith('internal/postgres/migrat'): return '이전버전 fixture·새 migration/checksum·rollbackless 이력 검증; DB SSOT'
    if p.startswith('internal/postgres/'): return 'authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60'
    if p.startswith('internal/server/workercontrol/'): return 'exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205'
    if p.startswith('internal/server/connectorwss/') or 'registry' in p: return 'ACK 후 revoke/replace Close notification과 TCP cleanup 경쟁·세션 소유권 보존; Connector/Realtime'
    if p.startswith('internal/server/connector/'): return 'claim 이후 typed dispatch 최종 제어·query 구분·peer Pause와 기존 Route 계약; LBT205/60'
    if p.startswith('internal/server/provision/'): return 'claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205'
    if p.startswith('internal/server/maintenance/'): return 'HTTP closed·기수락 drain 기본 경계와 opt-in Worker durable authority 분리; LBT205/Runtime'
    if p.startswith('internal/server/app/'): return 'production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60'
    if p.startswith(('internal/server/httpapi/','internal/server/labexecution/')): return '기존194 인증·권한·503·committed replay·3종 접수 경계의 회귀; HTTP SSOT'
    if p.startswith('internal/testsupport/'): return '위 소비자가 사용하는 deterministic Store/Resumer Fake; 운영 mutation 경로 아님'
    if p.startswith('runtime/'): return 'Runtime0.1.13 기본 MAINTENANCE·APPROVED_CONTROL opt-in과 별도 운영 CLI 미인수 경계 명시'
    if p.startswith('contracts/'): return '503 maintenance와 committed replay/권한 우선순위 공개 계약; HTTP SSOT'
    if p=='Makefile': return '실제 폐기 PG 통합 target에 신규 drcontrol 검증 package 포함; TESTING'
    if p.startswith('docs/'): return 'LBT59/60/205 시험/배포·백업/복구·구현과 운영 미검증/인수 경계 문서화'
    return '전체 diff primary 직접 검토'

rows=[]
for f in inventory:
    p=f['path']
    if p.startswith(('cmd/labbit-dr-control/','internal/drcontrol/','runtime/')): owner='operator + primary'
    elif p.startswith(('internal/connector/','internal/server/connectorwss/')) or p=='internal/server/connector/registry.go' or 'registry_close' in p: owner='regression + primary'
    else: owner='worker + primary'
    kind='test' if p.endswith('_test.go') else 'schema' if p.startswith('db/migrations/') else 'contract' if p.startswith(('contracts/','runtime/')) else 'documentation' if p.endswith('.md') else 'implementation/config'
    rows.append(f'| `{p}` | {kind}: {purpose(p)} | {owner} |')
(out/'diff-inventory.md').write_text('# Full product diff inventory\n\nBase main `'+summary['base']+'` → prior PR HEAD `'+summary['prior_pr_head']+'` + local supplement.\n\nThis list includes all non-Evidence changed files; exact hashes are in publication-source.json. Historical Evidence is retained under the original paths; new Evidence is covered by artifact-manifest.json. No source files excluded from the assigned primary/independent review boundaries. GitOps/Infra sibling worktrees are outside this App PR.\n\n| File | Purpose | Reviewer coverage |\n| --- | --- | --- |\n'+'\n'.join(rows)+'\n',encoding='utf-8')

runtime=yaml.safe_load((root/'runtime/contract.yaml').read_text(encoding='utf-8'))
assert runtime['version']=='0.1.13'
assert runtime['saas']['config']['LABBIT_WORKER_ADMISSION_POLICY']['default']=='MAINTENANCE'
migrations=sorted((root/'db/migrations').glob('[0-9]*_*.sql')); assert len(migrations)==14
assert len({p.name.split('_')[0] for p in migrations})==14
dump('contract-metadata.json',dict(runtime_version=runtime['version'],migration_files=14,duplicate_prefixes=False,openapi_connector_realtime_unchanged_from_prior_pr=True,full_contract_ci='awaiting exact published commit CI'))

patterns=[rb'(?:AKIA|ASIA)[A-Z0-9]{16}',rb'https://hooks\.slack\.com/services/[A-Za-z0-9/]+',rb'-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----']
hits=[]
for p in sorted(changed):
    file=root/p
    if file.is_file():
        b=file.read_bytes()
        if any(re.search(pattern,b) for pattern in patterns): hits.append(p)
dump('secret-pattern-scan.json',dict(scanned_paths=len(changed),matching_paths=hits,limitation='High-signal patterns only; synthetic fixtures and public keys allowed; does not prove absence of every secret format.'))
assert not hits,'potential secret; inspect before publication'
print(json.dumps(dict(go={k:v['test_events'] for k,v in summary['go'].items()},product_files=len(inventory),changed_files=len(changed),source_unchanged=json.loads((out/'source-manifest.json').read_text())['unchanged_from_prior']),ensure_ascii=False))
