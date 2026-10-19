from pathlib import Path
import hashlib, json, re

OUT = Path(__file__).resolve().parent
RUN = OUT.parent / 'lbt174-network-race-20261016'
ROOT = OUT.parent / 'labbit-connector-gates-20261015'
EXPECTED = 'a7d84f3071e07b5b92152743a96c58b92c288d58d912ceb6081bec221458fd51'

def load(path):
    return json.loads(path.read_text(encoding='utf8'))

def events(path):
    return [json.loads(line) for line in path.read_text(encoding='utf8').splitlines() if line.strip()]

report = {'scope': 'Read-only independent observations; parent execution and reviewer verification are distinct', 'runs': {}}
for name, folder in [('v1', RUN), ('v2', RUN / 'probe-v2'), ('v3', RUN / 'linux-v3')]:
    if not (folder / 'local-network-race.log').exists():
        continue
    rows = events(folder / 'local-network-race.log')
    observations = []
    for row in rows:
        output = row.get('Output', '')
        if output.startswith('LBT174_SSH_OBSERVATION '):
            value = json.loads(output.removeprefix('LBT174_SSH_OBSERVATION '))
            if value.get('kind') == 'internet-probe':
                observations.append(value)
    ab = []
    if (folder / 'route-counterfactual.log').exists():
        for line in (folder / 'route-counterfactual.log').read_text(encoding='utf8').splitlines():
            if line.startswith('{'):
                ab.append(json.loads(line))
    state = load(folder / 'run-state.json')
    matching = {p: hashlib.sha256((folder / p).read_bytes()).hexdigest() == digest for p, digest in state['overlaySHA256'].items()}
    assert all(matching.values()), (name, matching)
    result = load(folder / 'run-result.json') if (folder / 'run-result.json').exists() else None
    if result:
        assert result['sourceBefore']['sourceSHA256'] == result['sourceAfter']['sourceSHA256'] == EXPECTED
        assert result['sourceBefore']['mismatches'] == result['sourceAfter']['mismatches'] == 0
    joined = ''.join(row.get('Output', '') for row in rows)
    report['runs'][name] = {
        'childExit': result.get('exitCode') if result else None,
        'elapsedSeconds': result.get('elapsedSeconds') if result else None,
        'sourceBeforeAfterUnchanged': bool(result),
        'runStateHashesMatchCurrentInputs': matching,
        'internetProbeCount': len(observations),
        'successfulInternetProbes': sum(bool(row['success']) for row in observations),
        'failedInternetProbes': sum(not row['success'] for row in observations),
        'internetProbes': observations,
        'routeCounterfactual': ab,
        'trackedAbsenceMarkers': re.findall(r'exact tracked absence checked: resources=(\d+)', joined),
        'managementSGAbsenceMarkers': joined.count('Temporary Management security group GET absence confirmed'),
        'keypairAbsenceMarkers': joined.count('Temporary integration keypair GET absence confirmed'),
        'phaseResults': re.findall(r'Linux validation phase=([\w-]+) exit=(-?\d+) elapsed_ms=(\d+)', joined),
        'outerParentResults': [{k: row[k] for k in ('Action', 'Test', 'Elapsed') if k in row} for row in rows if row.get('Action') in ('pass', 'fail', 'skip') and row.get('Test')],
    }
    for field in ('postgresQueryExit', 'postgresStopExit'):
        if result and field in result:
            report['runs'][name][field] = result[field]
    if (folder / 'postgres-cleanup.log').exists():
        report['runs'][name]['temporaryPostgresDatabases'] = (folder / 'postgres-cleanup.log').read_text(encoding='utf8').strip()

ci_path = RUN / 'linux-v3/linux-ci.log'
if ci_path.exists():
    text = ci_path.read_text(encoding='utf8')
    ci_rows = []
    for line in text.splitlines():
        try:
            value = json.loads(line)
        except ValueError:
            continue
        if isinstance(value, dict):
            ci_rows.append(value)
    expected_parents = set()
    for path in (ROOT / 'internal/server/app').glob('*test.go'):
        expected_parents.update(re.findall(r'^func (TestC1\w+)\(t \*testing.T\)', path.read_text(encoding='utf8'), re.M))
    passed_parents = {v['Test'] for v in ci_rows if v.get('Action') == 'pass' and v.get('Test') in expected_parents}
    report['nativeLinux'] = {
        'source': 'linux-v3/linux-ci.log',
        'goVersionObserved': 'go version go1.27.1 linux/amd64' in text,
        'cgoEnabledObserved': '\n1\n' in text,
        'sourceProof': [v for v in ci_rows if 'sourceFiles' in v],
        'markers': {marker: text.count(marker) for marker in ('LBT174_LINUX_VET_PASS', 'LBT174_LINUX_BUILD_PASS', 'LBT174_LINUX_RACE_PASS')},
        'expectedParents': sorted(expected_parents), 'passedParents': sorted(passed_parents),
        'missingParents': sorted(expected_parents - passed_parents),
        'failures': [v for v in ci_rows if v.get('Action') == 'fail'],
        'skips': [v for v in ci_rows if v.get('Action') == 'skip'],
        'raceWarnings': text.count('WARNING: DATA RACE'),
        'testPassEvents': sum(v.get('Action') == 'pass' and 'Test' in v for v in ci_rows),
        'packagePassEvents': [v for v in ci_rows if v.get('Action') == 'pass' and 'Test' not in v],
        'boundary': 'Native Linux tagged C1 real-PG/Control harness uses a Mock Provider; this is not GitHub CI or KT API execution',
    }
(OUT / 'live-independent-observations.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
print(json.dumps({'runs': {k: {a: b for a, b in v.items() if a not in ('internetProbes', 'routeCounterfactual')} for k, v in report['runs'].items()}, 'nativeLinux': report.get('nativeLinux')}, ensure_ascii=False))
