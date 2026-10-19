from pathlib import Path
import datetime,hashlib,json,os,subprocess as sp,sys
OUT=Path(__file__).resolve().parent
FINAL=OUT/'final-verified'
ROOT=OUT.parent/'labbit-connector-gates-20261015'
def identity():
 manifest=json.loads((FINAL/'source-manifest.json').read_text(encoding='utf8'))
 files={p:hashlib.sha256((ROOT/p).read_bytes().replace(b'\r\n',b'\n')).hexdigest() for p in manifest['files']}
 assert files==manifest['files']
 return {'files':len(files),'sourceSHA256':hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()}
state={'startedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'sourceBefore':identity(),'binarySHA256':{name:hashlib.sha256((FINAL/name).read_bytes()).hexdigest() for name in ['workspace-sftp','workspacefs-tests','provider-tests','workspacefile-tests','credential-tests']}}
env=os.environ.copy()
env.update({'LABBIT_WORKSPACE_SFTP_HELPER_FILE':str(FINAL/'workspace-sftp'),'LABBIT_WORKSPACE_SFTP_TEST_BINARY':str(FINAL/'workspacefs-tests'),'LABBIT_LOCAL_LINUX_REGRESSION_DIR':str(FINAL)})
(OUT/'batch-input-identity.json').write_text(json.dumps(state,indent=2)+'\n',encoding='utf8')
try:
 result=sp.run([sys.executable,'-X','utf8',str(FINAL/'verify.py'),'live-all'],env=env,check=False)
 state['exitCode']=result.returncode
finally:
 state['finishedUTC']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 state['sourceAfter']=identity()
 state['sourceUnchanged']=state['sourceBefore']==state['sourceAfter']
 (OUT/'batch-input-result.json').write_text(json.dumps(state,indent=2)+'\n',encoding='utf8')
print(json.dumps(state),flush=True)
sys.exit(result.returncode)
