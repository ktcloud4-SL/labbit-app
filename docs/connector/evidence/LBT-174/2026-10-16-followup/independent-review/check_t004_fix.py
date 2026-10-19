import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time

out = Path(__file__).resolve().parent
repo = out.parent / 'labbit-connector-gates-20261015'
source = out / 'after-source'
paths = [
    'internal/server/preview/previewtest/peer.go',
    'internal/server/preview/previewtest/refused_windows.go',
    'internal/server/preview/previewtest/refused_other.go',
    'internal/server/preview/previewtest/refused_test.go',
]
old = out / 'before-t004-peer.go.txt'
if not old.exists():
    old.write_bytes((source / paths[0]).read_bytes())
files = {}
for rel in paths:
    data = (repo / rel).read_bytes().replace(b'\r\n', b'\n')
    (source / rel).write_bytes(data)
    files[rel] = hashlib.sha256(data).hexdigest()
env = os.environ.copy()
env['CGO_ENABLED'] = '1'
env['CC'] = 'C:/Users/user/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin/gcc.exe'
env['LABBIT_TEST_DATABASE_DSN'] = 'postgres://postgres@127.0.0.1:15432/postgres?sslmode=disable'
commands = [
    ('after-refused-helper-race.log', ['go', 'test', '-race', './internal/server/preview/previewtest', '-run', '^TestConnectionRefused', '-count=20', '-v', '-timeout=90s']),
    ('after-preview-refused-integration.log', ['go', 'test', '-tags', 'integration', './internal/server/app', '-run', '^TestPreviewCreateReportsAnApplicationThatIsNotListening$', '-count=5', '-v', '-timeout=90s']),
]
results = []
for logname, command in commands:
    start = time.time()
    result = subprocess.run(command, cwd=source, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (out / logname).write_bytes(result.stdout)
    results.append({'command': command, 'exit': result.returncode, 'seconds': round(time.time()-start, 3), 'log': logname, 'sha256': hashlib.sha256(result.stdout).hexdigest()})
current = {p:hashlib.sha256((repo/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in paths}
report = {'source': str(source), 'files': files, 'working_source_unchanged': files == current, 'runs': results, 'scope': 'External source copy; real disposable local PostgreSQL databases; no cloud calls. Four peer fixture files updated; unrelated first TCP test fix preserved for earlier semantic-mutant evidence.'}
(out / 't004-results.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps(report, ensure_ascii=False, indent=2))
assert report['working_source_unchanged'] and all(r['exit'] == 0 for r in results)
