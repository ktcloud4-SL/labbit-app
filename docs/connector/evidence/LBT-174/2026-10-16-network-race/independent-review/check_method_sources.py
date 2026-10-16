from pathlib import Path, PurePosixPath
import hashlib, json, subprocess, tarfile

OUT = Path(__file__).resolve().parent
RUN = OUT.parent / 'lbt174-network-race-20261016'
ROOT = OUT.parent / 'labbit-connector-gates-20261015'

def sha(data):
    return hashlib.sha256(data).hexdigest()

def load(path):
    return json.loads(path.read_text(encoding='utf8'))

manifest = load(RUN / 'source-manifest.json')
files = manifest['files']
current = {p: sha((ROOT / p).read_bytes().replace(b'\r\n', b'\n')) for p in files}
assert current == files
aggregate = sha(json.dumps(current, sort_keys=True).encode())
assert aggregate == manifest['sourceSHA256']

source_meta = load(RUN / 'linux-source-archive.json')
source_tgz = RUN / 'linux-source.tar.gz'
assert sha(source_tgz.read_bytes()) == source_meta['archiveSHA256']
assert source_tgz.stat().st_size == source_meta['archiveBytes']
go_meta = load(RUN / 'linux-go-download.json')
go_tgz = RUN / go_meta['file']['filename']
assert sha(go_tgz.read_bytes()) == go_meta['file']['sha256'] == go_meta['actualSHA256']
assert go_tgz.stat().st_size == go_meta['file']['size'] == go_meta['actualBytes']

tracked = set(subprocess.check_output(['git', 'ls-files'], cwd=ROOT, text=True).splitlines())
expected = {p for p in tracked | set(files) if (ROOT / p).is_file()}
with tarfile.open(source_tgz, 'r:gz') as archive:
    members = archive.getmembers()
    names = [m.name for m in members]
    assert len(names) == len(set(names))
    assert all(not PurePosixPath(n).is_absolute() and '..' not in PurePosixPath(n).parts for n in names)
    assert all(m.isfile() or m.isdir() for m in members), 'Unexpected link or device member'
    assert all(n == 'source' or n.startswith('source/') for n in names)
    payload = {m.name.removeprefix('source/'): archive.extractfile(m).read() for m in members if m.isfile()}
    source_paths = set(payload) - {'tested-source.json'} - {n for n in payload if n.startswith('vendor/')}
    assert source_paths == expected
    load_manifest = json.loads(payload['tested-source.json'])
    assert load_manifest == manifest
    archive_go = {p: sha(payload[p].replace(b'\r\n', b'\n')) for p in files}
    assert archive_go == files
    for p in source_paths:
        reference = (ROOT / p).read_bytes()
        if p in files:
            reference = reference.replace(b'\r\n', b'\n')
        assert payload[p] == reference, p
    vendor_paths = {n.removeprefix('vendor/'): data for n, data in payload.items() if n.startswith('vendor/')}
    disk_vendor = {p.relative_to(RUN / 'vendor').as_posix(): p.read_bytes() for p in (RUN / 'vendor').rglob('*') if p.is_file()}
    assert vendor_paths == disk_vendor
    assert len(vendor_paths) == source_meta['vendoredFiles']
    assert not any(any(part.lower() in ('.local', '.git', '.ssh') for part in PurePosixPath(n).parts) for n in names)
    assert not any(PurePosixPath(n).name.lower() in ('clouds.yaml', 'clouds.yml', 'clouds-public.yaml', 'secure.yaml', 'id_rsa', 'id_ed25519') for n in names)
    migrations = [p for p in source_paths if p.startswith('db/migrations/') and p.endswith('.sql')]
    assert migrations

state = load(RUN / 'run-state.json')
overlay_checks = {p: sha((RUN / p).read_bytes()) == h for p, h in state['overlaySHA256'].items()}
assert all(overlay_checks.values())
historical = ROOT / 'docs/connector/evidence/LBT-174/2026-10-16-followup'
seal = load(historical / 'package-checksums.json')
historical_files = {p.relative_to(historical).as_posix() for p in historical.rglob('*') if p.is_file()} - {'package-checksums.json'}
assert historical_files == set(seal['files'])
historical_mismatches = [p for p, digest in seal['files'].items() if sha((historical / p).read_bytes()) != digest]
assert not historical_mismatches
result = {
    'scope': 'Independent read-only archive, source, vendor and overlay identity verification; no cloud calls or source changes',
    'sourceFiles': len(files), 'sourceSHA256': aggregate, 'currentSourceMismatches': 0,
    'sourceArchiveBytes': source_tgz.stat().st_size, 'sourceArchiveSHA256': source_meta['archiveSHA256'],
    'sourceArchiveTrackedAndGoFiles': len(expected), 'sourceArchiveSourceMismatches': 0,
    'vendorFiles': len(vendor_paths), 'vendorDiskMismatches': 0, 'migrationSQLFiles': len(migrations),
    'toolchainArchiveBytes': go_tgz.stat().st_size, 'toolchainArchiveSHA256': go_meta['actualSHA256'],
    'toolchainOfficialMetadataURL': go_meta['officialMetadataURL'],
    'toolchainMetadataBoundary': 'Compared local archive bytes with the saved official metadata response; this script makes no network call',
    'unsafeTarPathsOrLinks': 0, 'privateDirectoryOrCredentialBasenameEntries': 0,
    'overlaySHA256MatchesRunState': overlay_checks,
    'frozenFollowupEvidenceFiles': len(historical_files), 'frozenFollowupEvidenceSealMismatches': historical_mismatches,
    'frozenFollowupSealSHA256': sha((historical / 'package-checksums.json').read_bytes()),
    'secretCheckBoundary': 'Path exclusions only; content secret screening is a separate check',
}
(OUT / 'method-source-verification.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
print(json.dumps(result, ensure_ascii=False))
