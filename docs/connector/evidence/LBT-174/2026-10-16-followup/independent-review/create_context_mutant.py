import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
source = root / 'after-source'
path = source / 'internal/connector/vmaccess/tcp.go'
raw = path.read_text(encoding='utf-8')
old = 'ctx, cancel := context.WithTimeout(ctx, m.config.SetupTimeout)'
assert raw.count(old) == 1
altered = raw.replace(old, 'ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.config.SetupTimeout)')
mutant = root / 'tcp-caller-deadline-ignored-mutant.go'
mutant.write_text(altered, encoding='utf-8', newline='\n')
(root / 'tcp-caller-deadline-ignored-overlay.json').write_text(json.dumps({'Replace': {str(path): str(mutant)}}, indent=2), encoding='utf-8')
(root / 'context-mutant-manifest.json').write_text(json.dumps({'source':str(path), 'source_lf_sha256':hashlib.sha256(raw.encode()).hexdigest(), 'mutation':'Ignore the caller deadline/cancel, retain production configured 10s setup timeout', 'mutant_lf_sha256':hashlib.sha256(altered.encode()).hexdigest()}, indent=2), encoding='utf-8')
