from pathlib import Path
import datetime,gzip,hashlib,json,re,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
FINAL=OUT/'final-verified'
def load(p): return json.loads(p.read_text(encoding='utf8'))
def text(p): return p.read_text(encoding='utf8',errors='replace')
def digest(b): return hashlib.sha256(b).hexdigest()
def events(p):
 rows=[]
 for line in text(p).splitlines():
  try:
   row=json.loads(line)
   if isinstance(row,dict): rows.append(row)
  except ValueError: pass
 return rows
def summarize(p):
 rows=events(p)
 return {'passEvents':sum(e.get('Action')=='pass' and 'Test' in e for e in rows),'topLevelPass':sorted(e['Test'] for e in rows if e.get('Action')=='pass' and 'Test' in e and '/' not in e['Test']),'fail':[e for e in rows if e.get('Action')=='fail'],'skip':[e for e in rows if e.get('Action')=='skip'],'raceWarning':'WARNING: DATA RACE' in text(p),'packagePass':sum(e.get('Action')=='pass' and 'Test' not in e for e in rows)}
manifest=load(FINAL/'source-manifest.json')
actual={p:digest((ROOT/p).read_bytes().replace(b'\r\n',b'\n')) for p in manifest['files']}
assert actual==manifest['files']
go={p:h for p,h in actual.items() if p.endswith('.go') or p in ['go.mod','go.sum']}
source={'allFiles':len(actual),'allSHA256':digest(json.dumps(actual,sort_keys=True).encode()),'goModuleFiles':len(go),'goModuleSHA256':digest(json.dumps(go,sort_keys=True).encode()),'primaryHEAD':sp.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip(),'primaryBranch':sp.check_output(['git','branch','--show-current'],cwd=ROOT).decode().strip(),'primaryStagedPaths':sp.check_output(['git','diff','--cached','--name-only'],cwd=ROOT).decode().splitlines()}
assert not source['primaryStagedPaths']
summary={'recordedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source':source,'primaryGo':load(FINAL/'go-test-summary.json'),'windowsRace':load(FINAL/'go-race-summary.json'),'extraServerPreviewRace':load(FINAL/'go-race-server-preview-summary.json'),'latestMainGo':load(OUT/'latest-main-check/go-test-summary.json'),'latestMainPostgres':load(OUT/'latest-main-check/postgres-integration-summary.json'),'latestMainSource':load(OUT/'latest-main-check/source-manifest.json')['sourceSHA256']}
reconcile=OUT/'latest-main-reconcile-check'
summary['latestReconcileMain']={'go':load(reconcile/'go-test-summary.json'),'postgres':load(reconcile/'postgres-integration-summary.json'),'source':load(reconcile/'source-manifest.json'),'databaseOwner':load(reconcile/'database-owner-result.json'),'webBridge':load(reconcile/'web-identity-bridge.json')}
assert summary['latestReconcileMain']['databaseOwner']['exitCode']==0 and summary['latestReconcileMain']['databaseOwner']['stopExit']==0
assert summary['latestReconcileMain']['databaseOwner']['sourceUnchanged'] and text(reconcile/'postgres-cleanup.log').strip()=='0'
for run in ['go-test','go-vet','go-build','postgres-vet','postgres-integration']:
 result=load(reconcile/(run+'-result.json'))
 assert result['exitCode']==0 and result['sourceUnchanged']
for scope in ['go','postgres']:
 assert not summary['latestReconcileMain'][scope]['testFail'] and not summary['latestReconcileMain'][scope]['packageFail']
native=summarize(FINAL/'linux-ci.log')
native.update({'markers':{s:s in text(FINAL/'linux-ci.log') for s in ['LBT174_LINUX_VET_PASS','LBT174_LINUX_BUILD_PASS','LBT174_LINUX_RACE_PASS']},'run':load(FINAL/'native-run-result.json')})
expected=sorted(set(re.findall(r'func (TestC1\w+)\(', '\n'.join(text(p) for p in (ROOT/'internal/server/app').glob('*test.go')))))
assert native['topLevelPass']==expected and len(expected)==12
assert not native['fail'] and not native['skip'] and not native['raceWarning'] and all(native['markers'].values())
assert native['run']['exitCode']==0 and native['run']['sourceAfter']['mismatches']==0
assert native['run']['sourceAfter']['sourceSHA256']==source['allSHA256']
assert text(FINAL/'postgres-cleanup.log').strip()=='0' and native['run']['postgresStopExit']==0
summary['nativeLinux']=native
for folder,prefix,policy in [(FINAL,'native','ON'),(OUT/'off-lifecycle','probe','OFF')]:
 lifecycle=summarize(folder/(prefix+'-live.log'))
 lifecycle['run']=load(folder/(prefix+'-run-result.json'))
 phases={}
 for p in sorted(folder.glob('*.log')):
  if p.name.startswith(('internet-first-','after-reset-first-','renew-','after-reboot-renew-','after-reset-renew-')) or p.name=='after-reboot.log':
   rows=events(p);samples=[r for r in rows if 'connectCode' in r]
   assert samples,p.name
   for r in samples:
    if policy=='ON': assert r['connectCode']==0 and len(r['defaultRoutes'])==1 and r['defaultRoutes'][0]['role']=='lab' and r['socketSourceRole']=='lab'
    else: assert r['connectCode']!=0 and r['defaultRoutes']==[] and r['routeBefore']==[] and r['routeAfter']==[]
   phases[p.stem]={'samples':len(samples),'renewedRoles':[r['renewedRole'] for r in rows if 'renewedRole' in r]}
 assert len([n for n in phases if n.startswith('internet-first-')])==12
 assert len([n for n in phases if n.startswith('after-reset-first-')])==12
 for stage in ['', 'after-reboot-','after-reset-']:
  for order in ['lab-first','management-first']:
   assert set(phases[stage+'renew-'+order]['renewedRoles'])=={'lab','management'}
 assert 'after-reboot' in phases
 raw=text(folder/(prefix+'-live.log'))
 assert 'actual reboot boot-id change, authenticated SSH recovery, immutable Reset g2' in raw
 expectedResources=16 if policy=='ON' else 14
 assert f'exact tracked absence checked: resources={expectedResources}' in raw
 assert 'Temporary Management security group GET absence confirmed' in raw
 assert 'Temporary integration keypair GET absence confirmed' in raw
 lifecycle['trackedResourcesConfirmedAbsent']=expectedResources
 assert lifecycle['run']['exitCode']==0 and not lifecycle['fail'] and not lifecycle['skip']
 assert lifecycle['run']['sourceAfter']['mismatches']==0
 assert lifecycle['run']['sourceAfter']['sourceSHA256']==source['allSHA256']
 lifecycle['routingPhases']=phases
 summary['internet'+policy]=lifecycle
live=summarize(FINAL/'local-openstack-all.log')
live['run']=load(FINAL/'local-openstack-all-result.json')
expected=sorted(set(re.findall(r'func (TestOpenStack\w+)\(', '\n'.join(text(p) for p in (ROOT/'internal/connector/provider/openstack').glob('*test.go')))))
assert live['topLevelPass']==expected and len(expected)==12
assert live['run']['exitCode']==0 and live['run']['sourceUnchanged']
assert live['run']['sourceSHA256']==source['goModuleSHA256']
assert not live['fail'] and not live['skip'] and not live['raceWarning']
requiredMarkers=[
 'Control OPEN -> outbound Preview WSS -> exact ATTACH -> actual SSH forwarding -> VM HTTP 200 twice; sequential tunnel contract PASS',
 'Concurrent session isolation, Control CLOSE, actual PTY/SFTP after Preview close, APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE PASS',
 'Actual Control Reset gen1->gen2 and Cleanup: old WSS closed, logical sessions removed, stale targets denied, fresh generation HTTP PASS',
 'Control contains lifecycle metadata only; HTTP/PTY bytes and synthetic credential not present PASS',
]
raw=text(FINAL/'local-openstack-all.log')
outputs='\n'.join(e.get('Output','') for e in events(FINAL/'local-openstack-all.log'))
assert all(marker in outputs for marker in requiredMarkers)
live['requiredPreviewMarkers']=requiredMarkers
summary['localOpenStackAll']=live
(OUT/'final-validation-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
print(json.dumps({'source':source['allSHA256'],'nativePassEvents':native['passEvents'],'nativeParentTests':len(native['topLevelPass']),'ON':'PASS','OFF':'PASS','fullOpenStackParentTests':len(live['topLevelPass']),'fullOpenStackPassEvents':live['passEvents'],'primaryStagedPaths':0}),flush=True)
