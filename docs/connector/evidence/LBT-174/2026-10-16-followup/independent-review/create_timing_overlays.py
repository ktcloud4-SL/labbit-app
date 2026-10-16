import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
source = root.parent / 'lbt174-final-independent-review-0051d8d' / 'reconstruction-isolated'
probes = [
    ('realtime', 'internal/server/realtime/data.go',
     '\tattached.Store(true)\n\tlog.Info("Connector Terminal Data attach", "resumed", resumed)',
     '\tattached.Store(true)\n\t// Independent review scheduling probe: ATTACHED can arrive before its log.\n\ttime.Sleep(75 * time.Millisecond)\n\tlog.Info("Connector Terminal Data attach", "resumed", resumed)'),
    ('vmaccess', 'internal/connector/vmaccess/access_test.go',
     '\t\t\t\tdefer conn.Close()\n\t\t\t\tcc, channels, requests, err := ssh.NewServerConn(conn, config)',
     '\t\t\t\tdefer conn.Close()\n\t\t\t\t// Independent review scheduling probe: healthy SSH readiness may take 75ms.\n\t\t\t\ttime.Sleep(75 * time.Millisecond)\n\t\t\t\tcc, channels, requests, err := ssh.NewServerConn(conn, config)'),
]
manifest = {'source': str(source), 'probes': []}
for name, rel, old, new in probes:
    original = (source / rel).read_text(encoding='utf-8')
    assert original.count(old) == 1, (name, 'anchor count')
    altered = original.replace(old, new)
    target = root / (name + '-timing-overlay.go')
    target.write_text(altered, encoding='utf-8', newline='\n')
    overlay = root / (name + '-overlay.json')
    overlay.write_text(json.dumps({'Replace': {str(source / rel): str(target)}}, indent=2), encoding='utf-8')
    manifest['probes'].append({'name': name, 'path': rel, 'original_lf_sha256': hashlib.sha256(original.encode()).hexdigest(), 'overlay_lf_sha256': hashlib.sha256(altered.encode()).hexdigest(), 'delay_ms': 75})
(root / 'timing-probe-manifest.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
print(json.dumps(manifest, indent=2))
