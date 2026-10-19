import hashlib
import json
from pathlib import Path
import shutil

root = Path(__file__).resolve().parent
original = root.parent / 'lbt174-final-independent-review-0051d8d' / 'reconstruction-isolated'
working = root.parent / 'labbit-connector-gates-20261015'
target = root / 'after-source'
assert not target.exists(), 'Preserve the previous probe source'
shutil.copytree(original, target, ignore=shutil.ignore_patterns('.git'))
changed = ['internal/server/realtime/trace_propagation_test.go', 'internal/server/realtime/trace_log_test.go', 'internal/connector/vmaccess/tcp_test.go']
manifest = {'source_base': str(original), 'working_tree': str(working), 'copied_files': []}
for rel in changed:
    data = (working / rel).read_text(encoding='utf-8').encode()
    (target / rel).write_bytes(data)
    manifest['copied_files'].append({'path': rel, 'lf_sha256': hashlib.sha256(data).hexdigest()})
for probe in json.loads((root / 'timing-probe-manifest.json').read_text())['probes']:
    name, rel = probe['name'], probe['path']
    overlay = root / (name + '-timing-overlay.go')
    (root / ('after-' + name + '-overlay.json')).write_text(json.dumps({'Replace': {str(target / rel): str(overlay)}}, indent=2), encoding='utf-8')
(root / 'after-source-manifest.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
print(json.dumps(manifest, indent=2))
