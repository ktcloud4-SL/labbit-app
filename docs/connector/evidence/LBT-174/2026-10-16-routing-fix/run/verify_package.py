from pathlib import Path
import datetime, gzip, hashlib, json, re, subprocess as sp, sys, urllib.parse

OUT = Path(__file__).resolve().parent
ROOT = OUT.parent / 'labbit-connector-gates-20261015'
DEST = ROOT / 'docs/connector/evidence/LBT-174/2026-10-16-routing-fix'

def digest(b): return hashlib.sha256(b).hexdigest()
def save(name, value):
    (DEST / name).write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
def unpack(p):
    raw = p.read_bytes()
    return gzip.decompress(raw) if p.suffix == '.gz' else raw

provenance = json.loads((DEST / 'raw-provenance.json').read_text(encoding='utf8'))
raw_errors = []
for row in provenance:
    actual = unpack(DEST / row['artifact'])
    if len(actual) != row.get('artifactUncompressedBytes', row['rawBytes']) or digest(actual) != row.get('artifactUncompressedSHA256', row['rawSHA256']):
        raw_errors.append(row['artifact'])

frozen = []
for folder in ['2026-10-16-followup', '2026-10-16-network-race']:
    base = DEST.parent / folder
    seal = json.loads((base / 'package-checksums.json').read_text(encoding='utf8'))
    errors = [rel for rel, expected in seal['files'].items() if digest((base / rel).read_bytes()) != expected]
    frozen.append({'folder': folder, 'files': len(seal['files']), 'errors': errors})

manifest = json.loads((OUT / 'source-manifest.json').read_text(encoding='utf8'))
actual = {rel: digest((ROOT / rel).read_bytes().replace(b'\r\n', b'\n')) for rel in manifest['files']}
source_errors = [rel for rel, expected in manifest['files'].items() if actual[rel] != expected]
inventory = json.loads((DEST / 'code-inventory.json').read_text(encoding='utf8'))
inventory_errors = [row['path'] for row in inventory if digest((DEST / row['artifact']).read_bytes()) != row['rawSHA256'] or digest((ROOT / row['path']).read_bytes()) != row['rawSHA256']]
reconstructed = {}
exports = {row['path']: DEST / row['artifact'] for row in inventory}
for rel in manifest['files']:
    raw = exports[rel].read_bytes() if rel in exports else sp.check_output(['git', 'show', 'HEAD:' + rel], cwd=ROOT)
    reconstructed[rel] = digest(raw.replace(b'\r\n', b'\n'))
reconstruction_errors = [rel for rel in actual if reconstructed[rel] != actual[rel]]
state = {'rawArtifacts': len(provenance), 'rawErrors': raw_errors, 'frozenPackages': frozen,
         'sourceFiles': len(actual), 'sourceErrors': source_errors,
         'sourceSHA256': digest(json.dumps(actual, sort_keys=True).encode()),
         'changedSourceFiles': len(inventory), 'inventoryErrors': inventory_errors,
         'reconstructionErrors': reconstruction_errors,
         'goSourcesInEvidence': len(list(DEST.rglob('*.go')))}
save('package-verification.json', state)
assert not raw_errors and not source_errors and not inventory_errors and not reconstruction_errors
assert all(not row['errors'] for row in frozen) and state['goSourcesInEvidence'] == 0

protected = []
key = ROOT / '.local/lbt174/local-fixture-key'
if key.exists():
    raw = key.read_bytes().strip()
    protected.append(('SSH private key complete', raw))
    protected.extend(('SSH private key body', part) for part in raw.splitlines() if len(part) > 40 and not part.startswith(b'-----'))
cloud = Path(r'C:\Users\user\.config\openstack\clouds.yaml')
if cloud.exists():
    import yaml
    def walk(value):
        if isinstance(value, dict):
            for name, item in value.items():
                if isinstance(item, str) and len(item) >= 8 and any(s in str(name).lower() for s in ('password', 'secret', 'token', 'application_credential_id')):
                    protected.append(('protected cloud ' + str(name), item.encode()))
                else: walk(item)
        elif isinstance(value, list):
            for item in value: walk(item)
    walk(yaml.safe_load(cloud.read_text(encoding='utf8')))
patterns = [('privateKeyHeader', rb'-----BEGIN (?:OPENSSH |RSA |EC |)PRIVATE KEY-----'),
            ('githubToken', rb'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b'),
            ('awsAccessKey', rb'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b')]
exact = []; generic = []; files = 0
for p in DEST.rglob('*'):
    if not p.is_file() or p.name == 'package-checksums.json': continue
    files += 1; raw = unpack(p)
    exact.extend({'path': p.relative_to(DEST).as_posix(), 'kind': name} for name, secret in protected if secret and secret in raw)
    generic.extend({'path': p.relative_to(DEST).as_posix(), 'kind': name} for name, pattern in patterns if re.search(pattern, raw))
save('secret-screen.json', {'filesScanned': files, 'protectedValueCandidatesCompared': len(protected),
     'exactProtectedInputMatches': exact, 'genericPatternCandidates': generic,
     'limits': 'Exact comparison to available protected inputs plus heuristic patterns; no protected values recorded. Not proof of absence of every secret.'})
assert not exact and not generic

missing = []
for p in DEST.rglob('*.md'):
    for target in re.findall(r'\[[^\]]+\]\(([^)\n]+)\)', p.read_text(encoding='utf8')):
        target = target.strip('<>')
        if '://' in target or target.startswith('#'): continue
        target = urllib.parse.unquote(target.split('#')[0])
        if target and not (p.parent / target).exists(): missing.append({'file': p.relative_to(DEST).as_posix(), 'target': target})
save('markdown-verification.json', {'brokenLinks': missing})
assert not missing, missing
if '--draft' in sys.argv:
    print(json.dumps({'draftOnly': True, 'rawArtifacts': len(provenance), 'sourceFiles': len(actual),
          'changedSourceFiles': len(inventory), 'frozenPreviousFiles': sum(r['files'] for r in frozen),
          'protectedInputsCompared': len(protected), 'secretMatches': len(exact), 'brokenMarkdownLinks': 0}), flush=True)
    sys.exit(0)
paths = sorted(p for p in DEST.rglob('*') if p.is_file() and p.name != 'package-checksums.json')
seal = {'recordedUTC': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'files': {p.relative_to(DEST).as_posix(): digest(p.read_bytes()) for p in paths}}
save('package-checksums.json', seal)
assert all(digest((DEST / rel).read_bytes()) == expected for rel, expected in seal['files'].items())
print(json.dumps({'sealedFiles': len(paths), 'rawArtifacts': len(provenance), 'sourceFiles': len(actual),
      'changedSourceFiles': len(inventory), 'frozenPreviousFiles': sum(r['files'] for r in frozen),
      'protectedInputsCompared': len(protected), 'secretMatches': len(exact),
      'brokenMarkdownLinks': len(missing), 'checksumMismatches': 0}), flush=True)
