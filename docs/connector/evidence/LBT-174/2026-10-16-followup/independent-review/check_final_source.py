import hashlib
import json
from pathlib import Path
import subprocess

out = Path(__file__).resolve().parent
repo = out.parent / 'labbit-connector-gates-20261015'
new_manifest = out.parent / 'lbt174-followup-20261016/verified/source-manifest.json'
old_manifest = out.parent / 'lbt174-final-audit-20261016/source-manifest.json'
current = json.loads(new_manifest.read_text())
previous = json.loads(old_manifest.read_text())

def git(*args):
    return subprocess.check_output(['git', *args], cwd=repo).decode().strip()

paths = set(git('ls-files', '--', '*.go', 'go.mod', 'go.sum').splitlines())
paths.update(git('ls-files', '--others', '--exclude-standard', '--', '*.go').splitlines())
files = {p:hashlib.sha256((repo/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in sorted(paths)}
identity_diff = sorted(p for p in set(files)|set(current['files']) if files.get(p)!=current['files'].get(p))
compared = sorted(p for p in set(previous['files'])|set(files) if previous['files'].get(p)!=files.get(p))
products = [p for p in compared if not p.endswith('_test.go')]
changed = set(git('diff', 'HEAD', '--name-only').splitlines())
changed.update(git('ls-files','--others','--exclude-standard','--','internal').splitlines())
go_changes = sorted(p for p in changed if p.endswith('.go'))
ssot_paths = git('ls-files','--','contracts','runtime','db/migrations','TESTING.md','AGENTS.md','CONTRIBUTING.md').splitlines()
ssot_diff=[]
for p in ssot_paths:
    committed=subprocess.check_output(['git','show','HEAD:'+p],cwd=repo).replace(b'\r\n',b'\n')
    if committed!=(repo/p).read_bytes().replace(b'\r\n',b'\n'): ssot_diff.append(p)
probe = json.loads((out/'probe-results.json').read_text())
probe_compared = dict(probe['copied_after_source_files'])
probe_compared[probe['bounded_tcp_test']['path']] = probe['bounded_tcp_test']['lf_sha256']
probe_diff=[p for p,h in probe_compared.items() if files.get(p)!=h]
result = {
 'base': current['base'], 'head':git('rev-parse','HEAD'), 'main':git('rev-parse','origin/main'), 'branch':git('branch','--show-current'),
 'manifest':str(new_manifest), 'claimed_aggregate':current['sourceSHA256'], 'computed_aggregate':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest(),
 'files':len(files), 'manifest_mismatches':identity_diff,
 'previous_aggregate':previous['sourceSHA256'], 'changes_since_previous_source':compared, 'product_changes_since_previous_source':products,
 'whole_feature_go_changes':go_changes, 'whole_feature_go_change_count':len(go_changes), 'ssot_changed_paths':ssot_diff,
 'reviewed_probe_source_mismatches':probe_diff,
}
(out/'final-source-alignment.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(result,ensure_ascii=False,indent=2))
assert not identity_diff and not probe_diff and not ssot_diff and not products
assert result['computed_aggregate']==current['sourceSHA256']
assert len(compared)==7 and len(go_changes)==22 and len(files)==498
