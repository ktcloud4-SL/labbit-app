import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
rows = [
    ('before-realtime.log', 1, 'expected failure', 3),
    ('before-vmaccess.log', 1, 'expected failure', 3),
    ('after-realtime.log', 0, 'first fix with 75ms scheduling fault', 10),
    ('after-vmaccess.log', 0, 'first fix with 75ms healthy handshake delay', 10),
    ('mutant-caller-deadline.log', 0, 'undesired mutant survivor: 50.08s', 1),
    ('bounded-normal.log', 0, 'bounded fix with 75ms healthy handshake delay', 10),
    ('bounded-mutant.log', 1, 'expected mutant rejection at 2.02s', 3),
    ('cleanup-absence-race.log', 0, 'GET absence helper race regression', 10),
    ('linux-gate-race.log', 0, 'Linux required tests gate race regression', 10),
]
results = {'scope':'Local loopback/fixture only; no reviewer cloud calls', 'runs':[]}
for name, status, meaning, repetitions in rows:
    data = (root / name).read_bytes()
    results['runs'].append({'log':name, 'observed_process_exit':status, 'meaning':meaning, 'requested_count':repetitions, 'sha256':hashlib.sha256(data).hexdigest()})
paths = ['internal/server/realtime/trace_propagation_test.go', 'internal/server/realtime/trace_log_test.go', 'internal/connector/provider/openstack/integration_test.go', 'internal/connector/provider/openstack/integration_cleanup_test.go', 'internal/connector/provider/openstack/workspace_file_integration_test.go', 'internal/connector/provider/openstack/workspace_file_integration_gate_test.go']
results['copied_after_source_files'] = {p:hashlib.sha256((root / 'after-source' / p).read_text(encoding='utf-8').encode()).hexdigest() for p in paths}
results['bounded_tcp_test'] = json.loads((root / 'bounded-test-manifest.json').read_text())
(root / 'probe-results.json').write_text(json.dumps(results, indent=2), encoding='utf-8')
print(json.dumps({'logs':len(rows),'source_files':len(paths)+1}))
