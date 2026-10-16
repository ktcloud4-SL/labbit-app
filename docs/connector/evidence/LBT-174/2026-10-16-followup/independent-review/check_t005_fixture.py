import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

out = Path(__file__).resolve().parent
repo = out.parent / 'labbit-connector-gates-20261015'
source = out / 'after-source'
for name in ['before-workspace-exec-race.log', 'after-workspace-exec-race.log', 't005-results.json']:
    original = out / name
    preserved = out / ('v1-fixture-error-' + name)
    if original.exists() and not preserved.exists():
        preserved.write_bytes(original.read_bytes())
rel = 'internal/connector/provider/openstack/workspace_file_integration_test.go'
previous = out / 'before-t005-workspace_file_integration_test.go.txt'
if not previous.exists():
    previous.write_bytes((source / rel).read_bytes())
data = (repo / rel).read_bytes().replace(b'\r\n', b'\n')
(source / rel).write_bytes(data)
probe = out / 'review_workspace_exec_test.go'
formatted = subprocess.check_output(['gofmt', str(probe)])
probe.write_bytes(formatted)
probe_rel = 'internal/connector/provider/openstack/review_workspace_exec_test.go'
(source / probe_rel).write_bytes(formatted)
before_overlay = out / 'before-t005-overlay.json'
old_behavior = out / 'before-t005-combined-output.go'
start = data.index(b'func workspaceFixtureOutput(')
end = data.index(b'func workspaceAssertError(', start)
old_body = b'''// Reviewer adapter: the original helper called CombinedOutput inside Setup.
// Its new return-value wrapper is kept only to use the same black-box probes.
func workspaceFixtureOutput(t *testing.T, ctx context.Context, m *vmaccess.Manager, target vmaccess.Target, command string) ([]byte, error) {
    t.Helper()
    s := realOpenSession(t, ctx, m, target)
    defer s.Close()
    var output []byte
    err := s.Setup(ctx, func(raw *ssh.Session) error {
        var err error
        output, err = raw.CombinedOutput(command)
        return err
    })
    return output, err
}
'''
old_behavior.write_bytes(data[:start] + old_body + data[end:])
before_overlay.write_text(json.dumps({'Replace': {str(source / rel): str(old_behavior)}}, indent=2), encoding='utf-8')
env = os.environ.copy()
env['CGO_ENABLED'] = '1'
env['CC'] = 'C:/Users/user/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin/gcc.exe'
commands = [
    ('before-workspace-exec-race.log', ['go', 'test', '-race', '-overlay', str(before_overlay), './internal/connector/provider/openstack', '-run', '^TestReviewWorkspaceExec', '-count=3', '-v', '-timeout=60s'], 1),
    ('after-workspace-exec-race.log', ['go', 'test', '-race', './internal/connector/provider/openstack', '-run', '^TestReviewWorkspaceExec', '-count=10', '-v', '-timeout=60s'], 0),
]
results = []
for logname, command, expected in commands:
    start = time.time()
    result = subprocess.run(command, cwd=source, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (out / logname).write_bytes(result.stdout)
    results.append({'command': command, 'exit': result.returncode, 'expected_exit': expected, 'seconds': round(time.time()-start,3), 'log': logname, 'sha256': hashlib.sha256(result.stdout).hexdigest()})
report = {'fixture': rel, 'lf_sha256': hashlib.sha256(data).hexdigest(), 'working_source_unchanged': data == (repo/rel).read_bytes().replace(b'\r\n',b'\n'), 'probe': str(probe), 'probe_lf_sha256': hashlib.sha256(formatted).hexdigest(), 'runs':results, 'scope':'Real loopback SSH peer; no cloud calls. Start acknowledgement and command exit separated by latch; sibling channel must start before first command release. Cancellation checks exact channel close and shared transport reuse.'}
(out / 't005-results.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps(report, ensure_ascii=False, indent=2))
assert report['working_source_unchanged'] and all(r['exit'] == r['expected_exit'] for r in results)
assert b'running fixture command retained SSH setup admission' in (out / 'before-workspace-exec-race.log').read_bytes()
assert b'[build failed]' not in (out / 'before-workspace-exec-race.log').read_bytes()
