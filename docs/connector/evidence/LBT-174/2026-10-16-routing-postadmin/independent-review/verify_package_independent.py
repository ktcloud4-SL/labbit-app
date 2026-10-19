from pathlib import Path, PurePosixPath
import datetime, gzip, hashlib, json, re, subprocess, urllib.parse
import yaml

HERE = Path(__file__).resolve().parent
TMP = HERE.parent
ROOT = TMP / 'labbit-connector-gates-20261015'
PACKAGE = ROOT / 'docs/connector/evidence/LBT-174/2026-10-16-routing-postadmin'
BASE = '0051d8d6803fd447f7b690dd895a0a928c3c7302'
GO = 'C:/Users/user/AppData/Local/Programs/Go-1.27.1/go/bin/go.exe'

def sha(raw): return hashlib.sha256(raw).hexdigest()
def load(path): return json.loads(path.read_text(encoding='utf8'))
def git(*args): return subprocess.check_output(['git', *args], cwd=ROOT)
def content(path):
    raw = path.read_bytes()
    return gzip.decompress(raw) if path.suffix == '.gz' else raw
def export_markdown(raw):
    def replacement(match):
        target = match.group(2)
        if '://' in target or target.startswith('#'): return match.group(0)
        base, separator, anchor = target.partition('#')
        if base.endswith('.go'): base += '.txt'
        elif base.endswith('.log'): base += '.gz'
        return match.group(1) + base + (separator + anchor if separator else '') + ')'
    return re.sub(r'(\[[^\]]+\]\()([^\)\n]+)\)', replacement, raw.decode('utf8')).encode('utf8')

errors = []
provenance = load(PACKAGE / 'raw-provenance.json')
snapshot = {p.relative_to(PACKAGE).as_posix(): sha(p.read_bytes()) for p in sorted(PACKAGE.rglob('*')) if p.is_file()}
seen_artifacts = set()
gzip_count = transformed_markdown = 0
for row in provenance:
    artifact = row['artifact']
    if artifact in seen_artifacts: errors.append(['duplicate artifact', artifact])
    seen_artifacts.add(artifact)
    assert '..' not in PurePosixPath(artifact).parts and not PurePosixPath(artifact).is_absolute()
    original = TMP / row['original']
    original_raw = original.read_bytes()
    raw = content(PACKAGE / artifact)
    if len(original_raw) != row['rawBytes'] or sha(original_raw) != row['rawSHA256']:
        errors.append(['original provenance mismatch', artifact])
    if len(raw) != row['artifactUncompressedBytes'] or sha(raw) != row['artifactUncompressedSHA256']:
        errors.append(['export provenance mismatch', artifact])
    expected = export_markdown(original_raw) if original.suffix == '.md' else original_raw
    if expected != raw: errors.append(['unexplained transformation', artifact])
    if row['transformation']: transformed_markdown += 1
    if artifact.endswith('.gz'):
        gzip_count += 1
        if int.from_bytes((PACKAGE / artifact).read_bytes()[4:8], 'little') != 0:
            errors.append(['non deterministic gzip mtime', artifact])

manifest = load(PACKAGE / 'run/final-verified/source-manifest.json')
inventory = load(PACKAGE / 'code-inventory.json')
exports = {row['path']: row for row in inventory}
tracked = set(git('diff', '--name-only', BASE, '--', 'internal', 'runtime', 'contracts').decode().splitlines())
untracked = set(git('ls-files', '--others', '--exclude-standard', '--', 'internal', 'runtime', 'contracts').decode().splitlines())
if set(exports) != tracked | untracked: errors.append(['inventory paths', sorted(set(exports) ^ (tracked | untracked))])
for row in inventory + load(PACKAGE / 'context-inventory.json'):
    artifact_raw = (PACKAGE / row['artifact']).read_bytes()
    if sha(artifact_raw) != row['rawSHA256'] or artifact_raw != (ROOT / row['path']).read_bytes():
        errors.append(['changed/context source mismatch', row['path']])
unchanged = sorted(set(manifest['files']) - set(exports))
batch = subprocess.check_output(['git', 'cat-file', '--batch'], input=''.join(BASE + ':' + p + '\n' for p in unchanged).encode(), cwd=ROOT)
offset = 0
base_bytes = {}
for rel in unchanged:
    end = batch.index(b'\n', offset)
    header = batch[offset:end].split()
    assert len(header) == 3 and header[1] == b'blob', (rel, header)
    size = int(header[2]); start = end + 1
    base_bytes[rel] = batch[start:start+size]
    assert batch[start+size:start+size+1] == b'\n'
    offset = start + size + 1
assert offset == len(batch)
current = {}
for rel, expected in manifest['files'].items():
    live = (ROOT / rel).read_bytes().replace(b'\r\n', b'\n')
    restored = ((PACKAGE / exports[rel]['artifact']).read_bytes() if rel in exports else base_bytes[rel]).replace(b'\r\n', b'\n')
    current[rel] = sha(live)
    if sha(live) != expected or restored != live: errors.append(['source reconstruction mismatch', rel])
aggregate = sha(json.dumps(current, sort_keys=True).encode())
assert aggregate == '7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31'
if (PACKAGE / 'tracked-diff.patch').read_bytes() != git('diff', BASE, '--', 'internal', 'runtime', 'contracts'):
    errors.append(['tracked patch mismatch'])
scope = load(HERE / 'source-scope-index.json')
if {r['path'] for r in scope['files']} != set(exports): errors.append(['review scope inventory mismatch'])

previous = []
for folder in ['2026-10-16-followup', '2026-10-16-network-race', '2026-10-16-routing-fix']:
    base = PACKAGE.parent / folder
    seal = load(base / 'package-checksums.json')
    mismatches = [p for p, expected in seal['files'].items() if sha((base / p).read_bytes()) != expected]
    previous.append({'folder': folder, 'sealedFiles': len(seal['files']), 'mismatches': mismatches})
    if mismatches: errors.append(['previous sealed evidence changed', folder])

protected = []
private = ROOT / '.local/lbt174/local-fixture-key'
if private.exists():
    raw = private.read_bytes().strip()
    protected.append(raw)
    protected.extend(p for p in raw.splitlines() if len(p) > 40 and not p.startswith(b'-----'))
cloud = Path('C:/Users/user/.config/openstack/clouds.yaml')
def values(obj):
    if isinstance(obj, dict):
        for name, value in obj.items():
            if isinstance(value, str) and len(value) >= 8 and any(x in str(name).lower() for x in ['password','secret','token','application_credential_id']):
                protected.append(value.encode())
            else: values(value)
    elif isinstance(obj, list):
        for value in obj: values(value)
if cloud.exists(): values(yaml.safe_load(cloud.read_text(encoding='utf8')))
patterns = [rb'-----BEGIN (?:OPENSSH |RSA |EC |)PRIVATE KEY-----', rb'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b', rb'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b']
secret_matches = []; links = []; unsafe = []
for rel in snapshot:
    path = PACKAGE / rel
    raw = content(path)
    if any(value in raw for value in protected if value): secret_matches.append([rel, 'protected input'])
    if any(re.search(pattern, raw) for pattern in patterns): secret_matches.append([rel, 'heuristic'])
    if any(part in ['.git','node_modules','vendor','data','.local','latest-main-integration','latest-main-reconcile-integration'] for part in path.relative_to(PACKAGE).parts) or path.suffix == '.go':
        unsafe.append(rel)
    if path.suffix == '.md':
        for target in re.findall(r'\[[^\]]+\]\(([^)\n]+)\)', raw.decode('utf8')):
            target = target.strip('<>')
            if '://' in target or target.startswith('#'): continue
            target = urllib.parse.unquote(target.split('#')[0])
            if target and not (path.parent / target).exists(): links.append([rel, target])
if secret_matches: errors.append(['secret screen candidates', secret_matches])
if links: errors.append(['broken links', links])
if unsafe: errors.append(['unsafe/package discovery path', unsafe])
packages = subprocess.check_output([GO, 'list', './...'], cwd=ROOT).decode().splitlines()
baseline = load(TMP / 'lbt174-routing-postadmin-20261016/go-package-scope-before.json')['packages']
if packages != baseline: errors.append(['Go package discovery changed'])

report = {
    'recordedUTC': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'reviewedSnapshotFiles': len(snapshot), 'reviewedRawArtifacts': len(provenance),
    'gzipMembersValidated': gzip_count, 'transformedMarkdownValidated': transformed_markdown,
    'sourceFiles': len(current), 'sourceSHA256': aggregate, 'changedSourceFiles': len(exports),
    'untrackedGoFiles': sum(r['state'] == 'untracked' and r['path'].endswith('.go') for r in inventory),
    'baseSourceFilesReconstructed': len(unchanged), 'previousSealedPackages': previous,
    'protectedInputsCompared': len(protected), 'secretCandidates': secret_matches,
    'brokenMarkdownLinks': links, 'unsafePaths': unsafe, 'GoPackageCount': len(packages),
    'primaryHEAD': git('rev-parse', 'HEAD').decode().strip(),
    'primaryBranch': git('branch', '--show-current').decode().strip(),
    'stagedPaths': git('diff', '--cached', '--name-only').decode().splitlines(),
    'errors': errors,
    'limits': 'Snapshot before final reviewer export/report update/seal. Exact comparison to available protected inputs plus heuristic screening is not proof that every possible secret is absent. No cloud/service/product mutation; read-only Git and go list only.'
}
(HERE / 'package-independent-snapshot.json').write_text(json.dumps(snapshot, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
(HERE / 'package-independent-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
print(json.dumps({k: report[k] for k in ['reviewedSnapshotFiles','reviewedRawArtifacts','gzipMembersValidated','sourceFiles','changedSourceFiles','protectedInputsCompared','GoPackageCount','errors']}, ensure_ascii=False))
assert not errors
assert not report['stagedPaths'] and report['primaryHEAD'] == BASE
