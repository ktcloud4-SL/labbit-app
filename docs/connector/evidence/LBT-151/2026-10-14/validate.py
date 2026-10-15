"""Reproduce LBT-151 checks/builds. Cloud mutations are separate gated phases.

Required LABBIT_EVIDENCE_REPO/OUT; Go, GCC and Git on PATH. See README.md.
Use a fresh output directory for each run; raw failure logs are never replaced.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys

sys.dont_write_bytecode = True
repo = Path(os.environ['LABBIT_EVIDENCE_REPO']).resolve(strict=True)
# Import the reusable baseline directly: the 150 entrypoint runs on import.
spec = importlib.util.spec_from_file_location('baseline_validator', repo/'docs/connector/evidence/LBT-148/2026-10-14/validate.py')
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)

def source(label):
    names = set(v.git('ls-files', '*.go', 'go.mod', 'go.sum').splitlines())
    names.update(v.git('ls-files','--others','--exclude-standard','*.go').splitlines())
    files = [{'path':n,'sha256LF':hashlib.sha256((repo/n).read_bytes().replace(b'\r\n',b'\n')).hexdigest()} for n in sorted(names)]
    value = {'recordedAt':v.now(),'head':v.git('rev-parse','HEAD'),'base':v.git('merge-base','HEAD','origin/main'),'branch':v.git('branch','--show-current'),'goVersion':subprocess.check_output(['go','version']).decode().strip(),'files':files}
    v.save('compiled-source-'+label+'.json',value)
    return value

def auto():
    v.auto()
    v.run('file-race',['go','test','-mod=readonly','-race','-json','-count=1','-timeout=5m','./internal/connector/filetransport','./internal/connector/workspacefile','./internal/connector/workspacefs','./internal/jsonnum','./internal/server/jsonnum'])
    v.run('fixture-race',['go','test','-mod=readonly','-race','-json','-count=20','-timeout=5m','./tools/ktcloud-d1-acceptance','./tools/ktcloud-op02-bootstrap'])
    v.run('file-repeat',['go','test','-mod=readonly','-race','-json','-count=20','-timeout=5m','-run','^(TestConcurrentRequestsAndCorrelatedClose|TestTimeoutPeerDisconnectAndCredentialRevokeCancelSFTP|TestCompletedAndCanceledIDsNeverRedialAcrossControlReconnect|TestFileOpenCapturesControlSenderAndActualDialCredential|TestReviewNoAdmissionAfterOldControlRetirement|TestReviewLateOldFrameNeverBindsNewControlOwner|TestFileOldOwnerCleanupPreservesNewOwnerAndRevocationUses4001|TestUnknownSaveOutcomeNeverReplaysAndRequiresFreshRead|TestReadCeilingAndOversizeSaveWithRequiredBinary)$','./internal/connector/filetransport','./internal/connector/wss'])

def build():
    env = dict(v.clean_env(),GOOS='linux',GOARCH='amd64',CGO_ENABLED='0')
    binaries = {}
    for name,target in [('labbit-connector-ktcloud','./cmd/labbit-connector'),('lbt145-control-linux','./tools/ktcloud-d1-acceptance'),('labbit-workspace-sftp','./cmd/labbit-workspace-sftp')]:
        output = v.OUT/name
        v.run('build-'+name,['go','build','-mod=readonly','-buildvcs=false','-o',str(output),target],env=env)
        binaries[name] = hashlib.sha256(output.read_bytes()).hexdigest()
    v.run('build-bootstrap',['go','build','-mod=readonly','-buildvcs=false','-o',str(v.OUT/'bootstrap.exe'),'./tools/ktcloud-op02-bootstrap'],env=dict(v.clean_env(),GOOS='windows',GOARCH='amd64',CGO_ENABLED='0'))
    binaries['bootstrap.exe'] = hashlib.sha256((v.OUT/'bootstrap.exe').read_bytes()).hexdigest()
    v.save('binary-manifest.json',{'goVersion':subprocess.check_output(['go','version']).decode().strip(),'linux':{'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'},'bootstrap':{'GOOS':'windows','GOARCH':'amd64','CGO_ENABLED':'0'},'flags':['-mod=readonly','-buildvcs=false'],'sha256':binaries,'source':'compiled-source-build-before.json'})

mode = sys.argv[1]
before = source(mode+'-before')
try:
    if mode in ('auto','build'): globals()[mode]()
    elif mode in ('web','contracts'): getattr(v,mode)()
    else: raise SystemExit('Expected auto, build, web or contracts')
finally:
    after = source(mode+'-after')
    v.save(mode+'-source-stability.json',{'compiledSourceUnchanged':before['head']==after['head'] and before['files']==after['files'],'headBefore':before['head'],'headAfter':after['head']})
