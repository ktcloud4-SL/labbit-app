"""LBT-150 reproducible validation; explicit paths and opt-in local VM inputs.
Uses the tracked portable LBT-148 make-equivalent validator without modifying it.
"""
import hashlib
import importlib.util
import os
from pathlib import Path
import sys

sys.dont_write_bytecode = True
repo = Path(os.environ['LABBIT_EVIDENCE_REPO']).resolve(strict=True)
spec = importlib.util.spec_from_file_location('baseline_validator', repo/'docs/connector/evidence/LBT-148/2026-10-14/validate.py')
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)

def targeted():
    v.run('file-race', ['go','test','-mod=readonly','-race','-json','-count=1','-timeout=3m','./internal/connector/filetransport','./internal/connector/wss','./internal/connector/app','./internal/jsonnum','./internal/server/jsonnum'], env=v.clean_env())

def live():
    for name in ['OS_CLOUD','LABBIT_PROVIDER_CONFIG_FILE','LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE']:
        if not os.environ.get(name): raise SystemExit('Missing explicit live input: '+name)
    env = v.clean_env()
    build = env.copy()
    build.update(GOOS='linux',GOARCH='amd64',CGO_ENABLED='0')
    helper = v.OUT/'labbit-workspace-sftp'
    v.run('helper-build',['go','build','-mod=readonly','-buildvcs=false','-o',str(helper),'./cmd/labbit-workspace-sftp'],env=build)
    v.save('binary-manifest.json',{'head':v.git('rev-parse','HEAD'),'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0','helperSHA256':hashlib.sha256(helper.read_bytes()).hexdigest(),'sourceSnapshot':'source-live-before.json'})
    v.run('pre-discovery',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-run','^TestOpenStackM1Integration$','./internal/connector/provider/openstack'],env=dict(env,LABBIT_OPENSTACK_INTEGRATION='1'))
    try:
        v.run('file-transport-live',['go','test','-mod=readonly','-race','-json','-count=1','-timeout=15m','-run','^TestOpenStackFileTransportIntegration$','./internal/connector/provider/openstack'],env=dict(env,LABBIT_OPENSTACK_FILE_TRANSPORT_TEST='1',LABBIT_WORKSPACE_SFTP_HELPER_FILE=str(helper)))
    finally:
        v.run('post-discovery',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-run','^TestOpenStackM1Integration$','./internal/connector/provider/openstack'],env=dict(env,LABBIT_OPENSTACK_INTEGRATION='1'))

def residual():
    for name in ['OS_CLOUD','LABBIT_PROVIDER_CONFIG_FILE']:
        if not os.environ.get(name): raise SystemExit('Missing explicit read-only input: '+name)
    overlay=v.OUT/'overlay.json'
    v.save('overlay.json',{'Replace':{str(repo/'internal/connector/provider/openstack/residual150_audit_test.go'):str(Path(__file__).resolve().parent/'residual150.go.txt')}})
    v.run('fixture-residual',['go','test','-mod=readonly','-json','-count=1','-timeout=2m','-overlay',str(overlay),'-run','^TestLBT150ReadOnlyFixtureResiduals$','./internal/connector/provider/openstack'],env=dict(v.clean_env(),LABBIT_OPENSTACK_RESIDUAL_AUDIT='1'))

mode = sys.argv[1]
before = v.snapshot(mode+'-before')
try:
    if mode == 'auto':
        v.auto()
        targeted()
        v.run('workspace-race',['go','test','-mod=readonly','-race','-json','-count=1','-timeout=3m','./internal/connector/workspacefile','./internal/connector/workspacefs'],env=v.clean_env())
        v.run('file-repeat',['go','test','-mod=readonly','-race','-json','-count=20','-timeout=3m','-run','^(TestConcurrentRequestsAndCorrelatedClose|TestTimeoutPeerDisconnectAndCredentialRevokeCancelSFTP|TestCompletedAndCanceledIDsNeverRedialAcrossControlReconnect|TestFileOpenCapturesControlSenderAndActualDialCredential|TestReviewNoAdmissionAfterOldControlRetirement|TestReviewLateOldFrameNeverBindsNewControlOwner|TestFileOldOwnerCleanupPreservesNewOwnerAndRevocationUses4001|TestUnknownSaveOutcomeNeverReplaysAndRequiresFreshRead|TestReadCeilingAndOversizeSaveWithRequiredBinary)$','./internal/connector/filetransport','./internal/connector/wss'],env=v.clean_env())
    elif mode in ('web','contracts'): getattr(v,mode)()
    elif mode in ('live','targeted','residual'): globals()[mode]()
    else: raise SystemExit('Expected auto, web, contracts, targeted, live or residual')
finally:
    after = v.snapshot(mode+'-after')
    v.save(mode+'-source-stability.json',{'unchanged':before['head']==after['head'] and before['files']==after['files'],'headBefore':before['head'],'headAfter':after['head']})
