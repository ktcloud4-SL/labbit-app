from pathlib import Path
import collections, gzip, hashlib, json, re, subprocess
from urllib.parse import unquote

OUT = Path(__file__).resolve().parent
WORK = OUT.parent
ROOT = WORK / 'labbit-connector-gates-20261015'
PACKAGE = ROOT / 'docs/connector/evidence/LBT-174/2026-10-16-network-race'

def sha(data):
    return hashlib.sha256(data).hexdigest()

def load(path):
    return json.loads(path.read_text(encoding='utf8'))

manifest = load(PACKAGE / 'raw-provenance.json')
seen, errors, transformations = set(), [], []
for item in manifest:
    name = item['artifact']
    assert name not in seen
    seen.add(name)
    raw = (WORK / item['original']).read_bytes()
    stored = (PACKAGE / name).read_bytes()
    data = gzip.decompress(stored) if item['compression'] == 'gzip' else stored
    if len(raw) != item['rawBytes'] or sha(raw) != item['rawSHA256']:
        errors.append({'kind': 'external-raw', 'artifact': name})
    if len(data) != item['artifactUncompressedBytes'] or sha(data) != item['artifactUncompressedSHA256']:
        errors.append({'kind': 'artifact', 'artifact': name})
    expected = raw
    if item['transformation']:
        if item['transformation'].startswith('Diagnostic VM Lab gateway'):
            expected = raw.replace(b'198.24.0.1', b'<lab-gateway>')
        elif item['transformation'].startswith('Markdown relative links'):
            value = raw.decode('utf8')
            value = re.sub(r'(\]\([^):\n]+\.go)(\))', r'\1.txt\2', value)
            value = re.sub(r'(\]\([^):\n]+\.log)(\))', r'\1.gz\2', value)
            expected = value.encode('utf8')
        else:
            errors.append({'kind': 'unreviewed-transformation', 'artifact': name})
        transformations.append(name)
    if data != expected:
        errors.append({'kind': 'bytes-after-transformation', 'artifact': name})
assert not errors, errors

old = ROOT / 'docs/connector/evidence/LBT-174/2026-10-16-followup'
old_seal = load(old / 'package-checksums.json')
assert set(old_seal['files']) == {p.relative_to(old).as_posix() for p in old.rglob('*') if p.is_file()} - {'package-checksums.json'}
assert all(sha((old / p).read_bytes()) == digest for p, digest in old_seal['files'].items())

broken, pending = [], []
for path in PACKAGE.rglob('*.md'):
    for target in re.findall(r'(?<!!)\[[^\]]*\]\(([^\n)]+)\)', path.read_text(encoding='utf8')):
        if target.startswith(('http:', 'https:', '#', 'app:', 'codex:')):
            continue
        relative = unquote(target.strip('<>').split('#')[0])
        candidate = path.parent / relative
        if not candidate.exists():
            record = {'file': path.relative_to(PACKAGE).as_posix(), 'target': target}
            if candidate.resolve() == (PACKAGE / 'independent-review/FINAL_AUDIT.md').resolve():
                pending.append(record)
            else:
                broken.append(record)
assert not broken, broken
assert not list(PACKAGE.rglob('*.go'))

generic = [
    ('private-key', rb'-----BEGIN (?:OPENSSH |RSA |EC |)PRIVATE KEY-----'),
    ('github-token', rb'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b'),
    ('aws-key', rb'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b'),
    ('jwt', rb'\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\b'),
]
candidates, scanned = [], 0
for path in PACKAGE.rglob('*'):
    if not path.is_file():
        continue
    data = path.read_bytes()
    if path.suffix == '.gz':
        data = gzip.decompress(data)
    try:
        data.decode('utf8')
    except UnicodeDecodeError:
        continue
    scanned += 1
    for kind, pattern in generic:
        if re.search(pattern, data):
            candidates.append({'artifact': path.relative_to(PACKAGE).as_posix(), 'kind': kind})

ours = load(OUT / 'live-independent-observations.json')
summary = load(PACKAGE / 'run/linux-v3/linux-ci-summary.json')
native = ours['nativeLinux']
assert summary['gatePass'] and set(summary['requiredParents']) == set(native['expectedParents'])
assert set(summary['observedParents']) == set(native['passedParents'])
assert all(v == 'pass' for v in summary['observedParents'].values())
assert not summary['failures'] and not summary['skips'] and summary['dataRaceReports'] == 0
assert native['testPassEvents'] == 67 and len(native['passedParents']) == 12
assert all(count == 1 for count in native['markers'].values())
assert summary['rawSHA256'] == sha((WORK / 'lbt174-network-race-20261016/linux-v3/linux-ci.log').read_bytes())

cleanup = load(PACKAGE / 'cleanup-observations.json')
for row in cleanup['runs']:
    observed = ours['runs'][row['stage']]
    assert row['hostGoExit'] == observed['childExit']
    assert row['elapsedSeconds'] == observed['elapsedSeconds']
    assert row['trackedResources'] == 8 and observed['trackedAbsenceMarkers'] == ['8']
    assert len(row['markers']) == 3 and not row['cleanupErrors']
    assert observed['managementSGAbsenceMarkers'] == observed['keypairAbsenceMarkers'] == 1
    assert row['sourceBefore'] == row['sourceAfter'] and row['sourceBefore']['mismatches'] == 0
    if row['stage'] != 'v2':
        assert row['postgresResidualDBCount'] == observed['temporaryPostgresDatabases'] == '0'
        assert row['postgresQueryExit'] == row['postgresStopExit'] == 0
assert cleanup['finalPGStatusExit'] == 3 and cleanup['finalPGStatus'] == 'pg_ctl: no server running'

source = load(PACKAGE / 'run/source-manifest.json')
assert source['files'] == load(old / 'source-manifest.json')['files']
current = {p: sha((ROOT / p).read_bytes().replace(b'\r\n', b'\n')) for p in source['files']}
assert current == source['files']
current_aggregate = sha(json.dumps(current, sort_keys=True).encode())
assert current_aggregate == source['sourceSHA256']
actual_go = set(subprocess.check_output(['git', 'ls-files', '--', '*.go', 'go.mod', 'go.sum'], cwd=ROOT, text=True).splitlines())
actual_go |= set(subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '--', '*.go'], cwd=ROOT, text=True).splitlines())
assert actual_go == set(source['files'])
inventory = load(old / 'code-inventory.json')
changed_go = set(subprocess.check_output(['git', 'diff', '--name-only', '--', '*.go'], cwd=ROOT, text=True).splitlines())
changed_go |= set(subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '--', '*.go'], cwd=ROOT, text=True).splitlines())
assert len(changed_go) == 27
assert changed_go == {item['path'] for item in inventory}
assert all(current[item['path']] == item['sha256LF'] for item in inventory)
unchanged_contracts = subprocess.check_output(['git', 'diff', '--name-only', '--', 'go.mod', 'go.sum', 'contracts', 'runtime', 'db', 'web', '.github'], cwd=ROOT, text=True).strip()
assert not unchanged_contracts
assert not subprocess.check_output(['git', 'diff', '--cached', '--name-only'], cwd=ROOT, text=True).strip()
go = Path('C:/Users/user/AppData/Local/Programs/Go-1.27.1/go/bin/go.exe')
listed = subprocess.run([str(go), 'list', './...'], cwd=ROOT, capture_output=True, text=True)
(OUT / 'go-list-after-evidence.log').write_text(listed.stdout + listed.stderr, encoding='utf8')
assert listed.returncode == 0
listed_packages = listed.stdout.splitlines()
evidence_packages = [p for p in listed_packages if '/docs/connector/evidence/LBT-174/' in p]
assert not evidence_packages

report = {
    'scope': 'Independent frozen 111-snapshot comparison; no product/cloud/remote mutation',
    'rawArtifactsChecked': len(manifest), 'externalRawAndArtifactErrors': errors,
    'recomputedTransformations': transformations, 'gzipArtifacts': sum(v['compression'] == 'gzip' for v in manifest),
    'frozenFollowupFiles': len(old_seal['files']), 'frozenFollowupHashMismatches': 0,
    'markdownBrokenLinks': broken, 'expectedFinalReportLinkPendingCopy': pending,
    'goFilesInNewEvidence': 0, 'heuristicTextFilesScanned': scanned, 'heuristicSecretCandidates': candidates,
    'secretBoundary': 'Reviewer did not read protected input values. Root separately reports seven exact protected input comparisons with zero matches.',
    'sourceFiles': len(current), 'sourceSHA256': current_aggregate, 'goInputSetMismatches': 0,
    'wholeFeatureChangedGoFiles': len(changed_go), 'dependencyRuntimeContractDBWebCIDiff': unchanged_contracts,
    'stagedFiles': 0, 'nativeLinuxExactParents': len(native['passedParents']), 'nativeLinuxTestPassEvents': native['testPassEvents'],
    'goListExit': listed.returncode, 'goListPackages': len(listed_packages), 'lbt174EvidencePackages': evidence_packages,
    'nativeLinuxSummaryAndCleanupMatchRaw': True, 'nativeLinuxFailSkipRace': [0, 0, 0],
    'hostWrapperExits': {k: v['childExit'] for k, v in ours['runs'].items()},
    'pgFinalStatusBoundary': 'Rechecked generated metadata and its capture script; no live PG query was made by reviewer',
}
assert not candidates, candidates
(OUT / 'package-independent-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
print(json.dumps(report, ensure_ascii=False))
