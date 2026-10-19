import hashlib
import json
import difflib
from pathlib import Path
import subprocess

out = Path(__file__).resolve().parent
repo = out.parent / 'labbit-connector-gates-20261015'
manifest_path = out.parent / 'lbt174-followup-20261016/finalized/source-manifest.json'
manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
old = json.loads((out.parent / 'lbt174-final-audit-20261016/source-manifest.json').read_text(encoding='utf-8'))
intermediate = json.loads((out.parent / 'lbt174-followup-20261016/verified/source-manifest.json').read_text(encoding='utf-8'))
def git(*args):
    return subprocess.check_output(['git', *args], cwd=repo).decode().strip()
paths = set(git('ls-files', '--', '*.go', 'go.mod', 'go.sum').splitlines())
paths.update(git('ls-files', '--others', '--exclude-standard', '--', '*.go').splitlines())
files = {p:hashlib.sha256((repo/p).read_bytes().replace(b'\r\n', b'\n')).hexdigest() for p in sorted(paths)}
differences = sorted(p for p in set(files)|set(manifest['files']) if files.get(p) != manifest['files'].get(p))
since_old = sorted(p for p in set(files)|set(old['files']) if files.get(p) != old['files'].get(p))
since_8e9 = sorted(p for p in set(files)|set(intermediate['files']) if files.get(p) != intermediate['files'].get(p))
support_prefix = 'internal/server/preview/previewtest/'
non_test_followup = [p for p in since_old if not p.endswith('_test.go')]
product_followup = [p for p in non_test_followup if not p.startswith(support_prefix)]
changed = set(git('diff', 'HEAD', '--name-only').splitlines())
changed.update(git('ls-files', '--others', '--exclude-standard', '--', 'internal').splitlines())
go_changes = sorted(p for p in changed if p.endswith('.go'))
ssot_paths = git('ls-files', '--', 'contracts', 'runtime', 'db/migrations', 'TESTING.md', 'AGENTS.md', 'CONTRIBUTING.md', '.github/workflows').splitlines()
ssot_changed = [p for p in ssot_paths if subprocess.check_output(['git','show','HEAD:'+p],cwd=repo).replace(b'\r\n',b'\n') != (repo/p).read_bytes().replace(b'\r\n',b'\n')]
probe_files = dict(json.loads((out/'probe-results.json').read_text(encoding='utf-8'))['copied_after_source_files'])
bounded = json.loads((out/'probe-results.json').read_text(encoding='utf-8'))['bounded_tcp_test']
probe_files[bounded['path']] = bounded['lf_sha256']
probe_files.update(json.loads((out/'t004-results.json').read_text(encoding='utf-8'))['files'])
t005 = json.loads((out/'t005-results.json').read_text(encoding='utf-8'))
probe_files[t005['fixture']] = t005['lf_sha256']
probe_diff = sorted(p for p,h in probe_files.items() if files.get(p) != h)
fixture = (repo/'internal/connector/provider/openstack/workspace_file_fixture_test.go').read_bytes().replace(b'\r\n',b'\n')
reviewed = (out/'review_workspace_exec_test.go').read_bytes().replace(b'\r\n',b'\n')
expected = reviewed.replace(b'reviewWorkspaceResolver', b'workspaceExecFixtureResolver').replace(b'reviewWorkspaceExecPeer', b'workspaceExecFixturePeer').replace(b'TestReviewWorkspaceExec', b'TestWorkspaceFixtureExec').replace(b'SetupTimeout: time.Second', b'SetupTimeout: 5 * time.Second').replace(b'context.WithTimeout(context.Background(), 5*time.Second)', b'context.WithTimeout(context.Background(), 10*time.Second)').replace(b'context.WithTimeout(ctx, 300*time.Millisecond)', b'context.WithTimeout(ctx, time.Second)')
(out/'t005-adopted-regression.diff').write_text(''.join(difflib.unified_diff(reviewed.decode().splitlines(True),fixture.decode().splitlines(True),fromfile='external-review-probe',tofile='adopted-regression')),encoding='utf-8')
result = {'base':manifest['base'], 'head':git('rev-parse','HEAD'), 'main':git('rev-parse','origin/main'), 'branch':git('branch','--show-current'), 'files':len(files), 'aggregate':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest(), 'manifest_claim':manifest['sourceSHA256'], 'manifest_mismatches':differences, 'changes_since_956c':since_old, 'changes_since_8e9':since_8e9, 'non_test_followup_paths':non_test_followup, 'product_followup_paths':product_followup, 'whole_feature_go_changes':go_changes, 'whole_feature_go_change_count':len(go_changes), 'ssot_changed_paths':ssot_changed, 'probe_source_mismatches':probe_diff, 't005_repo_file_lf_sha256':hashlib.sha256(fixture).hexdigest(), 't005_algorithm_comparison':fixture == expected, 't005_diff':'t005-adopted-regression.diff'}
(out/'finalized-source-alignment.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(result,ensure_ascii=False,indent=2))
assert not differences and not product_followup and not ssot_changed and not probe_diff
assert result['aggregate'] == manifest['sourceSHA256'] == 'a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51'
assert len(files) == 502 and len(go_changes) == 27 and len(since_old) == 12
assert fixture == expected
