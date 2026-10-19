from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

out = Path(__file__).resolve().parent
root = out.parent / 'lbt174-followup-20261016'
results = []
for directory in [root, root/'verified', root/'sealed', root/'closing', root/'finalized']:
    for path in sorted(directory.glob('*-result.json')):
        value = json.loads(path.read_text(encoding='utf-8'))
        results.append({'file':path.relative_to(root).as_posix(),'name':value.get('name'),'exit':value.get('exitCode'),'source':value.get('sourceSHA256'),'unchanged':value.get('sourceUnchanged'),'seconds':value.get('seconds'),'command':value.get('command'),'sha256':hashlib.sha256(path.read_bytes()).hexdigest()})
logs = [
    'local-openstack-all.log', 'postgres-integration.log',
    'verified/go-test.log', 'sealed/go-test.log', 'sealed/postgres-integration.log',
    'sealed/local-internet-diagnostic.log', 'closing/local-workspace-linux-harness.log',
    'finalized/go-test.log', 'finalized/local-internet-unmodified.log',
]
summaries=[]
for name in logs:
    path=root/name
    if not path.exists():
        continue
    ended=[]
    nested_pass=[]
    for line in path.read_text(encoding='utf-8').splitlines():
        if not line.startswith('{'):
            continue
        event=json.loads(line)
        if event.get('Action') in ('pass','fail','skip'):
            ended.append({k:event[k] for k in ('Action','Package','Test','Elapsed') if k in event})
        text=event.get('Output','').strip()
        # Keep test status names only; do not copy output payloads or endpoints.
        if text.startswith('--- PASS: '):
            nested_pass.append(text)
    summaries.append({'file':name,'sha256_at_observation':hashlib.sha256(path.read_bytes()).hexdigest(),'test_pass_events':sum(e['Action']=='pass' and 'Test' in e for e in ended),'test_fail':[e for e in ended if e['Action']=='fail' and 'Test' in e],'test_skip':[e for e in ended if e['Action']=='skip' and 'Test' in e],'package_end':[e for e in ended if 'Test' not in e],'top_level_test_end':[e for e in ended if 'Test' in e and '/' not in e['Test']],'nested_pass_output':nested_pass if 'workspace-linux' in name else []})
report={'observed_utc':datetime.now(timezone.utc).isoformat(),'root':str(root),'results':results,'logs':summaries,'scope':'Read-only local execution records. Complete run is established by actual result JSON exit and final package events, never by partial PASS output. Original failures and sourceUnchanged=false remain present.'}
(out/'execution-observations.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps({'result_records':len(results),'logs':[{'file':s['file'],'pass':s['test_pass_events'],'fail':len(s['test_fail']),'skip':len(s['test_skip']),'package_pass':sum(e['Action']=='pass' for e in s['package_end']),'package_fail':sum(e['Action']=='fail' for e in s['package_end'])} for s in summaries]},ensure_ascii=False,indent=2))
