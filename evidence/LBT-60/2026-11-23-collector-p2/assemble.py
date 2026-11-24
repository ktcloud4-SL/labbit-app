"""Collect local offline logs/source hashes. No cloud, deployment or Git writes."""
from pathlib import Path
import collections
import datetime
import gzip
import hashlib
import json
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
BASE = '753eecbff737409a4c9cc8c542f3c6baa6acf0da'
HEAD = 'f5413c91282888115ad2b497d5c2183ab58f2e70'


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT).decode().splitlines()


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def write(name, value):
    (OUT / name).write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')


def summarize(path):
    tests, packages = collections.Counter(), collections.Counter()
    skips, failures, names = [], [], []
    for line in path.read_text(encoding='utf-8-sig').splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        action = event.get('Action')
        if action not in ('pass', 'fail', 'skip'):
            continue
        if 'Test' in event:
            tests[action] += 1
            names.append(event['Test'])
            if action == 'skip':
                skips.append({'package': event['Package'], 'test': event['Test']})
        else:
            packages[action] += 1
        if action == 'fail':
            failures.append({'package': event.get('Package'), 'test': event.get('Test')})
    return dict(tests=dict(tests), packages=dict(packages), skipped=skips, failed=failures,
                includes_new_signed_fail_case=any('WorkReconciled_FAIL' in name for name in names))


assert git('rev-parse', 'HEAD') == [HEAD], 'Record new baseline explicitly before reuse'
local = set(git('diff', '--name-only', 'HEAD')) | set(git('ls-files', '--others', '--exclude-standard'))
paths = set(git('diff', '--name-only', BASE, HEAD)) | local
records = []
for path in sorted(paths):
    if path.startswith(OUT.relative_to(ROOT).as_posix() + '/'):
        continue
    file = ROOT / path
    if not file.is_file():
        records.append(dict(path=path, deleted=True))
        continue
    raw = file.read_bytes()
    canonical = raw.replace(b'\r\n', b'\n')
    baseline_equal = False
    if path not in local:
        baseline_equal = subprocess.check_output(['git', 'show', f'{HEAD}:{path}'], cwd=ROOT).replace(b'\r\n', b'\n') == canonical
    records.append(dict(path=path, bytes=len(raw), sha256=sha(raw), canonical_lf_sha256=sha(canonical),
                        local_delta=path in local, baseline_canonical_unchanged=baseline_equal,
                        review='fresh local diff and affected consumers' if path in local else
                        'historical evidence only' if path.startswith('evidence/') else
                        'unchanged prior full-PR audit reused; not a new live validation'))
write('source-manifest.json', dict(base=BASE, head=HEAD, branch=git('branch', '--show-current')[0],
                                  uncommitted=True, files=records))

summaries = {}
for path in sorted((ROOT / '.local').glob('collector-p2-*')):
    if not path.is_file():
        continue
    with gzip.GzipFile(filename=str(OUT / (path.name + '.gz')), mode='wb', mtime=0) as archive:
        archive.write(path.read_bytes())
    if path.suffix == '.jsonl':
        summaries[path.name] = summarize(path)
write('test-summary.json', dict(go=summaries, web=dict(typecheck_exit=0, lint_exit=0, build_exit=0,
    tests_pass=493, test_files=35, max_workers=1, source='collector-p2-web-*.txt.gz'),
    current_scope='offline Windows Go1.27.1/Fake readers+signed artifacts; no production credentials or DB',
    exact_source_note='Full Go ran before the final test-only external FAIL assertion; final affected race includes it. '
                      'Product collector source is identical in both. Signed consumer case membership is recorded per log.',
    initial_failure='Fixture build type error preserved separately; then baseline product failed24 subcases+parent. '
                    'Do not count expected-red regression as a remaining final failure.',
    limits=['No PostgreSQL integration, no Linux execution or new remote CI for this patch',
            'No AWS/EKS/Provider, shared DB, Slack, migration, deployment, merge or push',
            'Go suite skips retained by exact test name; suites overlap and are not summed']))

# The checkout uses CRLF for many unchanged Go files. Check canonical content
# without rewriting hundreds of unrelated files, matching the previous audit.
go_files = sorted(set(git('ls-files', '*.go')) | {p for p in local if p.endswith('.go')})
bad = []
for path in go_files:
    raw = (ROOT / path).read_bytes().replace(b'\r\n', b'\n')
    formatted = subprocess.run(['gofmt'], input=raw, capture_output=True, check=True).stdout
    if formatted != raw:
        bad.append(path)
write('format-validation.json', dict(method='gofmt stdin compared to LF-normalized tracked/local Go, no source rewrite',
    files_checked=len(go_files), failures=bad,
    initial_raw_checkout_check='collector-p2-format.txt.gz includes CRLF-only names; not hidden or bulk-formatted'))
assert not bad, bad

review = Path(r'C:\Users\user\.codex\tmp')
for name in ('pr196-collector-p2-review-20261123.md', 'pr196-collector-p2-review-source-20261123.json'):
    file = review / name
    if file.is_file():
        shutil.copyfile(file, OUT / name)
for path in review.glob('pr196-collector-p2-review*20261123.jsonl'):
    with gzip.GzipFile(filename=str(OUT / (path.name + '.gz')), mode='wb', mtime=0) as archive:
        archive.write(path.read_bytes())

write('artifact-manifest.json', dict(created_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
    files=[dict(path=p.relative_to(OUT).as_posix(), bytes=p.stat().st_size, sha256=sha(p.read_bytes()))
           for p in sorted(OUT.rglob('*')) if p.is_file() and p.name != 'artifact-manifest.json']))
print(json.dumps({'summaries': summaries, 'canonical_format_files': len(go_files), 'format_failures': bad}, ensure_ascii=False))
