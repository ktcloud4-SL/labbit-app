"""Additional latest-main checks; never executes a cloud operation.

Run the Oct14 validate.py auto/web/contracts/build modes into fresh OUTs first.
This module only checks the newly inherited Backend boundary. Linux test
compilation is explicitly not Linux/PostgreSQL execution.
"""
import importlib.util
import os
from pathlib import Path
import sys

sys.dont_write_bytecode = True
repo = Path(os.environ['LABBIT_EVIDENCE_REPO']).resolve(strict=True)
spec = importlib.util.spec_from_file_location('baseline', repo/'docs/connector/evidence/LBT-148/2026-10-14/validate.py')
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)
before = v.snapshot('latest-main-before')
try:
    v.run('backend-default-race', ['go','test','-mod=readonly','-race','-json','-count=1','-timeout=5m','./internal/server/app','./internal/server/connector'])
    v.run('session-fence-repeat', ['go','test','-mod=readonly','-race','-json','-count=20','-timeout=5m','-run','^TestRouterPinnedSessionRejectsReplacement$','./internal/server/connector'])
    env = dict(v.clean_env(), GOOS='linux', GOARCH='amd64', CGO_ENABLED='0')
    v.run('linux-harness-vet', ['go','vet','-mod=readonly','-tags','backend_router_harness integration','./internal/server/app','./internal/server/connector'], env=env)
    v.run('linux-harness-build', ['go','build','-mod=readonly','-buildvcs=false','-tags','backend_router_harness','-o',str(v.OUT/'labbit-c1-server'),'./cmd/labbit-server'], env=env)
    v.run('linux-harness-test-compile', ['go','test','-mod=readonly','-c','-tags','backend_router_harness integration','-o',str(v.OUT/'app-harness.test'),'./internal/server/app'], env=env)
    v.save('not-executed.json', {'linuxTaggedHarnessExecution': 'NOT RUN: local host is Windows, no WSL distro/Docker/PostgreSQL test server. Linux vet/build/test compilation only; no substitution for POSIX gates or PostgreSQL behavior.', 'postgresIntegrationSuite': 'NOT RUN; no test DSN/server. No DB/runtime/HTTP/wire change in the proposed LBT151 diff.', 'actualCloudOct15': 'NOT RUN; no cloud resources created. Reuse Oct14 actual proof only for unchanged and binary-equivalent Connector/VM fixture scope.', 'c2FullBrowserBackendVM': 'NOT RUN; remains LBT22.'})
finally:
    after = v.snapshot('latest-main-after')
    v.save('latest-main-source-stability.json', {'unchanged': before['head']==after['head'] and before['files']==after['files'], 'headBefore':before['head'], 'headAfter':after['head']})
