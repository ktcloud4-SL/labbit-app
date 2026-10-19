import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
source = root / 'after-source'
working = root.parent / 'labbit-connector-gates-20261015'
rel = 'internal/connector/vmaccess/tcp_test.go'
fixed = (working / rel).read_text(encoding='utf-8')
target = root / 'after-T003-tcp_test.go'
target.write_text(fixed, encoding='utf-8', newline='\n')
for name, overlay_path in [('normal', 'after-vmaccess-overlay.json'), ('mutant', 'tcp-caller-deadline-ignored-overlay.json')]:
    overlay = json.loads((root / overlay_path).read_text())
    overlay['Replace'][str(source / rel)] = str(target)
    (root / ('bounded-' + name + '-overlay.json')).write_text(json.dumps(overlay, indent=2), encoding='utf-8')
(root / 'bounded-test-manifest.json').write_text(json.dumps({'path': rel, 'copied_from': str(working), 'lf_sha256':hashlib.sha256(fixed.encode()).hexdigest()}, indent=2), encoding='utf-8')
