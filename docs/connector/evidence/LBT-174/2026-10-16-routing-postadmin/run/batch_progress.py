from pathlib import Path
import json
p=Path(__file__).resolve().parent/'final-verified/local-openstack-all.log'
rows=[json.loads(line) for line in p.read_text(encoding='utf8').splitlines() if line.startswith('{')]
parents=[r for r in rows if 'Test' in r and '/' not in r['Test']]
passed=[r['Test'] for r in parents if r.get('Action')=='pass']
failed=[r['Test'] for r in rows if r.get('Action')=='fail' and 'Test' in r]
skipped=[r['Test'] for r in rows if r.get('Action')=='skip' and 'Test' in r]
started=[r['Test'] for r in parents if r.get('Action')=='run']
print(json.dumps({'parentPassed':len(passed),'passed':passed,'failed':failed,'skipped':skipped,'current':next((x for x in reversed(started) if x not in passed+failed+skipped),None)}),flush=True)
