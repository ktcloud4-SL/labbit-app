"""LBT-149 validation. Requires explicit repo/output paths; live opts in to
disposable local OpenStack VM creation/admin fixture installation/cleanup.
The tracked LBT-148 portable validator supplies make-equivalent checks.
"""
import hashlib
import importlib.util
import os
from pathlib import Path
import sys

sys.dont_write_bytecode = True
repo = Path(os.environ['LABBIT_EVIDENCE_REPO']).resolve(strict=True)
baseline = repo/'docs/connector/evidence/LBT-148/2026-10-14/validate.py'
spec = importlib.util.spec_from_file_location('baseline_validator', baseline)
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)
folder = Path(__file__).resolve().parent

def live():
    for name in ['OS_CLOUD','LABBIT_PROVIDER_CONFIG_FILE','LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE']:
        if not os.environ.get(name): raise SystemExit('Missing explicit live input: '+name)
    env = v.clean_env()
    build = env.copy()
    build.update(GOOS='linux',GOARCH='amd64',CGO_ENABLED='0')
    helper = v.OUT/'labbit-workspace-sftp'
    tests = v.OUT/'workspacefs-tests'
    v.run('helper-build',['go','build','-mod=readonly','-buildvcs=false','-o',str(helper),'./cmd/labbit-workspace-sftp'],env=build)
    v.run('linux-tests-build',['go','test','-mod=readonly','-c','-o',str(tests),'./internal/connector/workspacefs'],env=build)
    v.save('binary-manifest.json',{'head':v.git('rev-parse','HEAD'),'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0','binaries':[{'name':p.name,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in [helper,tests]],'sourceSnapshot':'source-live-before.json'})
    env.update(LABBIT_OPENSTACK_WORKSPACE_FILE_TEST='1',LABBIT_WORKSPACE_SFTP_HELPER_FILE=str(helper),LABBIT_WORKSPACE_SFTP_TEST_BINARY=str(tests))
    v.run('workspace-live',['go','test','-mod=readonly','-json','-count=1','-timeout=15m','-run','^TestOpenStackWorkspaceFileIntegration$','./internal/connector/provider/openstack'],env=env)

def residual():
    for name in ['OS_CLOUD','LABBIT_PROVIDER_CONFIG_FILE']:
        if not os.environ.get(name): raise SystemExit('Missing explicit read-only input: '+name)
    env = v.clean_env()
    env['LABBIT_OPENSTACK_RESIDUAL_AUDIT']='1'
    overlay = v.OUT/'overlay.json'
    v.save('overlay.json',{'Replace':{str(repo/'internal/connector/provider/openstack/residual149_audit_test.go'):str(folder/'residual149.go.txt')}})
    v.run('fixture-residual',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-overlay',str(overlay),'-run','^TestLBT149ReadOnlyResiduals$','./internal/connector/provider/openstack'],env=env)

mode = sys.argv[1]
before = v.snapshot(mode+'-before')
try:
    if mode == 'auto':
        v.auto()
        v.run('workspace-race',['go','test','-mod=readonly','-race','-json','-count=1','-timeout=3m','./internal/connector/workspacefile','./internal/connector/workspacefs'],env=v.clean_env())
    elif mode in ('web','contracts'):
        getattr(v,mode)()
    elif mode in ('live','residual'):
        globals()[mode]()
    else:
        raise SystemExit('Expected auto, web, contracts, live or residual')
finally:
    after = v.snapshot(mode+'-after')
    v.save(mode+'-source-stability.json',{'unchanged':before['head']==after['head'] and before['files']==after['files'],'headBefore':before['head'],'headAfter':after['head']})
