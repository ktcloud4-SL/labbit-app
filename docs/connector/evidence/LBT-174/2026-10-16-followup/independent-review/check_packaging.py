"""Independent, read-only Evidence check; writes only this external folder."""
from pathlib import Path
import collections, gzip, hashlib, io, json, re, subprocess, sys, tarfile
from datetime import datetime, timezone

sys.stdout.reconfigure(encoding='utf-8')
OUT = Path(__file__).resolve().parent
REPO = OUT.parent / 'labbit-connector-gates-20261015'
PKG = REPO / 'docs/connector/evidence/LBT-174/2026-10-16-followup'
ORIGINAL = OUT.parent / 'lbt174-followup-20261016'

def readj(p): return json.loads(p.read_text(encoding='utf-8-sig'))
def sha(b): return hashlib.sha256(b).hexdigest()
def lf(b): return b.replace(b'\r\n', b'\n')
def aggregate(files): return sha(json.dumps(files, sort_keys=True).encode())
def run(args, cwd):
    p = subprocess.run(args, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode: raise RuntimeError(f'command failed {args[:3]}: {p.stderr.decode(errors="replace")[:800]}')
    return p.stdout

manifest = readj(PKG / 'source-manifest.json')
expected = manifest['files']
errors = []
raw = readj(PKG / 'raw-provenance.json')
raw_by_source = {}
raw_checks = []
post_snapshot_reviewer_updates = []
for entry in raw:
    artifact = PKG / entry['artifact']
    data = gzip.decompress(artifact.read_bytes()) if artifact.suffix == '.gz' else artifact.read_bytes()
    actual = sha(data)
    if (len(data), actual) != (entry['rawBytes'], entry['rawSHA256']):
        errors.append({'kind': 'raw artifact mismatch', 'artifact': entry['artifact']})
    source = Path(entry['source'])
    parts = source.parts
    original = (OUT / Path(*parts[1:])) if parts[0] == 'independent-review' else ((ORIGINAL / Path(*parts[1:])) if parts[0] == 'first' else ((ORIGINAL / 'final' / Path(*parts[1:])) if parts[0] == 'intermediate' else ORIGINAL / source))
    original_same = None
    original_gzip = original.with_name(original.name + '.gz')
    if original.is_file() or original_gzip.is_file():
        original_data = original.read_bytes() if original.is_file() else gzip.decompress(original_gzip.read_bytes())
        original_same = sha(original_data) == actual
        if not original_same:
            difference = {'kind': 'original differs', 'artifact': entry['artifact']}
            # The checker itself is being finalized after its historical package copy.
            # Keep the difference explicit; the final publication refresh must copy it.
            if entry['source'] == 'independent-review/check_packaging.py':
                post_snapshot_reviewer_updates.append(difference)
            else:
                errors.append(difference)
    raw_checks.append({'source': entry['source'], 'artifact': entry['artifact'], 'rawBytes': len(data), 'sha256': actual, 'matchesOriginal': original_same})
    raw_by_source[entry['source']] = data

# Full code patch must recreate every input, including formerly untracked files.
stamp = datetime.now().strftime('%Y%m%d-%H%M%S')
reconstructed = OUT / ('package-reconstruction-' + stamp)
reconstructed.mkdir()
archive = run(['git', 'archive', '--format=tar', manifest['base']], REPO)
with tarfile.open(fileobj=io.BytesIO(archive), mode='r:') as tf:
    tf.extractall(reconstructed, filter='data')
run(['git', 'init', '-q'], reconstructed)
run(['git', 'apply', '--check', str(PKG / 'full-code-diff.patch')], reconstructed)
run(['git', 'apply', str(PKG / 'full-code-diff.patch')], reconstructed)
reconstructed_map = {p: sha(lf((reconstructed / p).read_bytes())) for p in expected}
reconstruction_diff = [p for p in expected if reconstructed_map[p] != expected[p]]
current_diff = [p for p in expected if sha(lf((REPO / p).read_bytes())) != expected[p]]
errors.extend({'kind': 'reconstruction source mismatch', 'file': p} for p in reconstruction_diff)
errors.extend({'kind': 'current source mismatch', 'file': p} for p in current_diff)
patch_paths = re.findall(r'^diff --git a/(.*?) b/.*$', (PKG / 'full-code-diff.patch').read_text(encoding='utf-8'), re.M)
inventory = readj(PKG / 'code-inventory.json')
if sorted(patch_paths) != sorted(x['path'] for x in inventory): errors.append({'kind': 'patch inventory mismatch'})

bridges = readj(PKG / 'stage-source-bridges.json')
stage_results = []
for stage in bridges['stages']:
    name = stage['stage']
    if '5ea' in name: path = PKG / stage['manifest']
    else: path = PKG / (('closing' if name.startswith('closing-') else name) + '/source-manifest.json')
    saved = readj(path)
    stage_map = saved['files']
    changes = sorted(p for p in set(stage_map) | set(expected) if stage_map.get(p) != expected.get(p))
    match = aggregate(stage_map) == saved['sourceSHA256'] == stage['sourceSHA256'] and len(stage_map) == stage['files'] and changes == sorted(stage['differentFromFinal'])
    if not match: errors.append({'kind': 'stage bridge mismatch', 'stage': name})
    stage_results.append({'stage': name, 'files': len(stage_map), 'aggregateMatches': match, 'differentFromFinal': changes})
sealed = readj(PKG / 'sealed/source-manifest.json')['files']
live = readj(PKG / 'closing-live-source-reconstruction.json')
rebuilt = dict(sealed)
helper = 'internal/connector/provider/openstack/workspace_file_integration_test.go'
rebuilt[helper] = expected[helper]
if rebuilt != live['files'] or aggregate(rebuilt) != live['sourceSHA256']:
    errors.append({'kind': '5ea reconstruction mismatch'})

event_checks = []
for path in sorted(PKG.rglob('*-events.json')):
    event_meta = readj(path)
    prefix = path.relative_to(PKG).as_posix().removesuffix('-events.json')
    raw_data = raw_by_source[prefix + '.log']
    events = []
    for line in raw_data.splitlines():
        try: obj = json.loads(line)
        except (json.JSONDecodeError, UnicodeDecodeError): continue
        if isinstance(obj, dict) and obj.get('Action') in ('pass', 'fail', 'skip'): events.append(obj)
    counts = collections.Counter((x['Action'], bool(x.get('Test'))) for x in events)
    got = {'testPass': counts['pass', True], 'testFail': counts['fail', True], 'testSkip': counts['skip', True], 'packagePass': counts['pass', False], 'packageFail': counts['fail', False]}
    normalized = {k: (len(v) if isinstance(v, list) else v) for k, v in event_meta.items() if k in got}
    if got != normalized: errors.append({'kind': 'event summary mismatch', 'file': path.relative_to(PKG).as_posix(), 'actual': got, 'summary': normalized})
    event_checks.append({'file': path.relative_to(PKG).as_posix(), **got})

cleanup = readj(PKG / 'cleanup-observations.json')
cleanup_checks = []
for cloud in cleanup['cloudRuns']:
    key = cloud['stage'] + '/' + cloud['sourceLog']
    rows = []
    for line in raw_by_source[key].splitlines():
        try: obj = json.loads(line)
        except (json.JSONDecodeError, UnicodeDecodeError): continue
        if isinstance(obj, dict): rows.append(obj)
    outputs = ''.join(row.get('Output', '') for row in rows)
    final = {row['Test']: row['Action'] for row in rows if row.get('Test') and row.get('Action') in ('pass', 'fail', 'skip')}
    markers = 0
    for item in cloud['topLevel']:
        if final.get(item['test']) != item['action']: errors.append({'kind': 'cleanup top-level action mismatch', 'file': key, 'test': item['test']})
        for marker in item['cleanupAndResourceMarkers']:
            markers += 1
            if marker not in outputs: errors.append({'kind': 'cleanup marker absent from original output', 'file': key, 'test': item['test']})
    cleanup_checks.append({'source': key, 'topLevelResultsCompared': len(cloud['topLevel']), 'markersCompared': markers})

broken_links = []
for path in PKG.rglob('*.md'):
    for target in re.findall(r'\]\(([^)]+)\)', path.read_text(encoding='utf-8-sig')):
        if target.startswith(('http:', 'https:', '#', 'codex:')): continue
        target_path = target.split('#')[0].strip('<>')
        if target_path and not (path.parent / target_path).exists(): broken_links.append({'file': path.relative_to(PKG).as_posix(), 'target': target_path})
errors.extend({'kind': 'broken Markdown link', **item} for item in broken_links)

go_paths = [p.relative_to(PKG).as_posix() for p in PKG.rglob('*.go')]
if go_paths: errors.append({'kind': 'executable Go artifact under Evidence', 'paths': go_paths})
docs_list = subprocess.run(['go', 'list', './docs/connector/evidence/LBT-174/...'], cwd=REPO, capture_output=True)
if docs_list.returncode or docs_list.stdout.strip(): errors.append({'kind': 'Go package contamination', 'exit': docs_list.returncode})

# Screen visible bytes, including decompressed logs, without recording candidate values.
patterns = {
    'private-key-header': re.compile(r'-----BEGIN (?:OPENSSH |RSA |EC |DSA |ENCRYPTED )?PRIVATE KEY-----'),
    'JWT-shaped-value': re.compile(r'\beyJ[A-Za-z0-9_-]{15,}\.[A-Za-z0-9_-]{15,}\.[A-Za-z0-9_-]{15,}\b'),
    'bearer-shaped-value': re.compile(r'(?i)\bBearer\s+[A-Za-z0-9._~-]{30,}'),
    'AWS-access-key-shaped': re.compile(r'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b'),
}
candidates = []
screened = 0
for path in PKG.rglob('*'):
    if not path.is_file(): continue
    data = gzip.decompress(path.read_bytes()) if path.suffix == '.gz' else path.read_bytes()
    try: text = data.decode('utf-8-sig')
    except UnicodeDecodeError: continue
    screened += 1
    for category, pattern in patterns.items():
        for match in pattern.finditer(text):
            candidates.append({'file': path.relative_to(PKG).as_posix(), 'line': text.count('\n', 0, match.start()) + 1, 'category': category})

result = {
    'checkedAtUTC': datetime.now(timezone.utc).isoformat(),
    'base': manifest['base'], 'sourceSHA256': manifest['sourceSHA256'],
    'reconstructionRoot': str(reconstructed), 'isolatedGitInit': True,
    'patchPaths': patch_paths, 'sourceFilesCompared': len(expected),
    'reconstructedAggregate': aggregate(reconstructed_map),
    'currentSourceDifferences': current_diff, 'reconstructedDifferences': reconstruction_diff,
    'rawArtifactsChecked': len(raw_checks), 'originalFilesCompared': sum(x['matchesOriginal'] is not None for x in raw_checks),
    'postSnapshotReviewerScriptUpdates': post_snapshot_reviewer_updates,
    'rawArtifacts': raw_checks, 'stageBridges': stage_results,
    'reconstructed5eaMatches': rebuilt == live['files'], 'eventSummaries': event_checks,
    'cleanupChecks': cleanup_checks, 'brokenLocalMarkdownLinks': broken_links,
    'goArtifactsUnderPackage': go_paths, 'goDocsListExit': docs_list.returncode,
    'secretScreen': {'textFilesChecked': screened, 'candidates': candidates, 'limit': 'Heuristic review only; protected inputs were not read by this reviewer. Author exact-input screening is separate.'},
    'errors': errors,
}
(OUT / 'packaging-independent-check.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({k:v for k,v in result.items() if k not in ('rawArtifacts', 'stageBridges', 'patchPaths', 'eventSummaries')}, ensure_ascii=False, indent=2))
assert not errors, errors
assert not candidates, 'Secret pattern candidates require manual review; values not recorded.'
assert len(expected) == 502 and len(patch_paths) == 27
assert aggregate(reconstructed_map) == manifest['sourceSHA256']
