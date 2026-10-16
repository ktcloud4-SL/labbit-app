from pathlib import Path
import ast, datetime, hashlib, json, subprocess, time, types

OUT = Path(__file__).resolve().parent
RUN = OUT.parent / 'lbt174-network-race-20261016'
identity = json.loads((RUN / 'safe-runner-identity.json').read_text(encoding='utf8'))

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

all_cases = []
sources = []
for version, item in enumerate(identity):
    original = RUN / item['original']
    source = RUN / item['safeRunner']
    assert sha(original) == item['originalSHA256']
    assert sha(source) == item['safeRunnerSHA256']
    tree = ast.parse(source.read_text(encoding='utf8'))
    index = next(i for i, node in enumerate(tree.body) if isinstance(node, ast.Try))
    prefix = tree.body[index - 2:index]
    assert len(prefix) == 2 and all(isinstance(node, ast.Assign) for node in prefix)
    code = compile(ast.Module(body=prefix + [tree.body[index]], type_ignores=[]), str(source), 'exec')
    sources.append({'source': item['safeRunner'], 'SHA256': sha(source), 'originalSHA256StillMatches': True})

    for mode in ('start-rejected', 'normal', 'go-exit1', 'go-timeout'):
        calls, saved = [], {}
        folder = OUT / f'safe-ownership-{version}-{mode}'
        folder.mkdir(exist_ok=True)

        def run(args, **kwargs):
            calls.append([str(arg) for arg in args])
            if mode == 'start-rejected' and args[-1] == 'start':
                raise subprocess.CalledProcessError(1, args)
            if args[0] == 'not-a-real-go':
                if mode == 'go-timeout':
                    raise subprocess.TimeoutExpired(args, 3700)
                if mode == 'go-exit1':
                    return types.SimpleNamespace(returncode=1)
            return types.SimpleNamespace(returncode=0)

        scope = {
            'OUT': folder, 'PG': Path('/not-a-real-postgres'), 'ROOT': Path('/not-a-real-repository'),
            'sp': types.SimpleNamespace(run=run, STDOUT=subprocess.STDOUT),
            'args': ['not-a-real-go', 'test'], 'env': {}, 'result': {},
            'start': time.monotonic(), 'time': time, 'datetime': datetime, 'json': json,
            'identity': lambda: {'sourceUnchanged': True},
            'save': lambda name, value: saved.update({name: dict(value)}),
            'print': lambda *args, **kwargs: None,
        }
        raised = None
        try:
            exec(code, scope)
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            raised = type(error).__name__
        result = saved['run-result.json']
        observed = {
            'source': item['safeRunner'], 'mode': mode,
            'startCalls': sum(args[-1] == 'start' for args in calls),
            'stopCalls': sum(args[-1] == 'stop' for args in calls),
            'testCalls': sum(args[0] == 'not-a-real-go' for args in calls),
            'queryCalls': sum('psql.exe' in args[0] for args in calls),
            'exception': raised, 'result': result,
        }
        assert observed['startCalls'] == 1
        if mode == 'start-rejected':
            assert observed['stopCalls'] == observed['queryCalls'] == observed['testCalls'] == 0
            assert observed['exception'] == result['runnerExceptionType'] == 'CalledProcessError'
            assert result['postgresStartedHere'] is False and result['postgresCleanupSkipped'] == 'not_started_here'
        else:
            assert observed['stopCalls'] == observed['queryCalls'] == observed['testCalls'] == 1
            assert result['postgresStartedHere'] is True
            assert result['postgresQueryExit'] == result['postgresStopExit'] == 0
            if mode == 'go-timeout':
                assert observed['exception'] == result['runnerExceptionType'] == 'TimeoutExpired'
            else:
                assert observed['exception'] is None
                assert result['exitCode'] == (1 if mode == 'go-exit1' else 0)
        all_cases.append(observed)

report = {
    'finding': 'F174-METHOD003', 'verdict': 'Fixed for the reported PG ownership boundary',
    'scope': 'Independent AST execution of both new safe runners with every subprocess stubbed; no PostgreSQL, Go child or cloud process runs',
    'sources': sources, 'casesPassed': len(all_cases), 'cases': all_cases,
    'limits': 'Does not execute live cloud cleanup, PG binary failures during cleanup or interrupted-process recovery; historical as-run runners stay unchanged',
}
(OUT / 'safe-runner-ownership-observations.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf8')
print(json.dumps({'verdict': report['verdict'], 'sources': sources, 'casesPassed': len(all_cases)}))
