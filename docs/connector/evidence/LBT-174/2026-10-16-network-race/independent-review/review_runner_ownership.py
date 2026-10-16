from pathlib import Path
import ast, datetime, hashlib, json, subprocess, time, types

OUT = Path(__file__).resolve().parent
SOURCE = OUT.parent / 'lbt174-network-race-20261016/run_validation.py'
raw = SOURCE.read_bytes()
(OUT / 'reviewed-run-validation.py.txt').write_bytes(raw)
tree = ast.parse(raw.decode('utf8'))
block = next(node for node in tree.body if isinstance(node, ast.Try))
code = compile(ast.Module(body=[block], type_ignores=[]), str(SOURCE), 'exec')

def probe(start_rejected):
    calls = []
    saved = {}
    folder = OUT / ('ownership-start-rejected' if start_rejected else 'ownership-normal')
    folder.mkdir(exist_ok=True)

    def run(args, **kwargs):
        calls.append([str(arg) for arg in args])
        if start_rejected and args[-1] == 'start':
            raise subprocess.CalledProcessError(1, args)
        return types.SimpleNamespace(returncode=0)

    scope = {
        'OUT': folder, 'PG': Path('/not-a-real-postgres'), 'ROOT': Path('/not-a-real-repository'),
        'sp': types.SimpleNamespace(run=run, STDOUT=subprocess.STDOUT),
        'args': ['not-a-real-go', 'test'], 'env': {}, 'result': {},
        'start': time.monotonic(), 'time': time, 'datetime': datetime, 'json': json,
        'identity': lambda: {'sourceUnchanged': True},
        'save': lambda name, value: saved.update({name: value}),
        'print': lambda *args, **kwargs: None,
    }
    raised = None
    try:
        exec(code, scope)
    except subprocess.CalledProcessError as error:
        raised = {'type': type(error).__name__, 'exitCode': error.returncode}
    return {
        'startRejected': start_rejected,
        'startCalls': sum(args[-1] == 'start' for args in calls),
        'stopCalls': sum(args[-1] == 'stop' for args in calls),
        'testCalls': sum(args[0] == 'not-a-real-go' for args in calls),
        'queryCalls': sum('psql.exe' in args[0] for args in calls),
        'exception': raised, 'resultSaved': 'run-result.json' in saved,
    }

cases = [probe(True), probe(False)]
assert cases[0]['startCalls'] == 1 and cases[0]['stopCalls'] == 1 and cases[0]['testCalls'] == 0
assert cases[1]['startCalls'] == 1 and cases[1]['stopCalls'] == 1 and cases[1]['testCalls'] == 1
result = {
    'scope': 'Execute only original try/finally AST with subprocess.run entirely stubbed; no PostgreSQL, Go child or cloud call is made',
    'source': SOURCE.as_posix(), 'sourceSHA256': hashlib.sha256(raw).hexdigest(),
    'finding': 'F174-METHOD003: failed PG start still invokes stop of the configured data directory',
    'cases': cases,
}
(OUT / 'runner-ownership-observations.json').write_text(json.dumps(result, indent=2) + '\n', encoding='utf8')
print(json.dumps(result))
