"""Export allowlisted LBT-151 evidence; protected inputs/binaries are excluded.

Usage: python export_evidence.py REPO VALIDATION_ROOT ATTEMPT1 [ATTEMPT2 ...] FINAL
Run after collect, residual, bootstrap cleanup and final baseline confirmation.
Original failures stay in the protected fixture directories. Published logs
retain byte hashes; IPv4 removal applies only to bootstrap/operator live logs.
"""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import json
import re
import subprocess
import sys
import zipfile

repo, validation, *fixtures = [Path(p).resolve(strict=True) for p in sys.argv[1:]]
final = fixtures[-1]
attempts = [('attempt-'+str(i+1), fixture) for i,fixture in enumerate(fixtures)]
auto_folder = 'auto-run6-final'
out = repo/'docs/connector/evidence/LBT-151/2026-10-14'
def read(p): return json.loads(p.read_text(encoding='utf-8-sig'))
def save(name, data): (out/name).write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
def sha(data): return hashlib.sha256(data).hexdigest()
def git(*args): return subprocess.check_output(['git',*args],cwd=repo,stderr=subprocess.DEVNULL).decode().strip()

tested = read(final/'build-final/compiled-source-build-before.json')
for f in tested['files']:
    if sha((repo/f['path']).read_bytes().replace(b'\r\n',b'\n')) != f['sha256LF']:
        raise SystemExit('Current compiled source differs: '+f['path'])
if not read(final/'build-final/build-source-stability.json')['compiledSourceUnchanged']:
    raise SystemExit('Final build source changed while compiling')
save('source-manifest.json',dict(tested,sourceVerifiedAgainstCurrentCheckout=True,
     identity='base HEAD plus complete LF source hashes; sources were uncommitted during execution',
     binaryManifest=read(final/'build-final/binary-manifest.json')))
save('binary-manifest.json',read(final/'build-final/binary-manifest.json'))

manifest=[]
typed_manifest=[]
def exported_log(p, name, redact=False):
    original=p.read_bytes()
    data=original
    replacements=0
    if redact:
        s=original.decode('utf-8-sig',errors='strict')
        s,replacements=re.subn(r'\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?\b','[IPv4 omitted]',s)
        if replacements: data=s.encode()
    # Reject accidental secret/body payloads before publication. This scan is
    # supplemented by the human/reviewer pass, not claimed as a DLP guarantee.
    if re.search(rb'BEGIN [A-Z ]*PRIVATE KEY|Authorization[:=].*Bearer|["\'](?:password|auth_token|access_token)["\']\s*:',data,re.I):
        raise SystemExit('Publication scan needs review: '+name)
    manifest.append({'path':name,'originalSha256':sha(original),'publishedSha256':sha(data),
       'bytes':len(data),'ipv4Redactions':replacements,
       'utf8BomRemoved':replacements>0 and original.startswith(b'\xef\xbb\xbf'),'originalPreserved':True})
    return data

with zipfile.ZipFile(out/'validation-logs.zip','w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
    for directory in sorted(validation.iterdir()):
        if not directory.is_dir(): continue
        for p in sorted(directory.iterdir()):
            if p.is_file() and p.suffix in ('.json','.log','.txt'):
                name='validation/'+directory.name+'/'+p.name
                z.writestr(name,exported_log(p,name))
    for label,fixture in attempts:
        for p in sorted(fixture.glob('*.log')):
            name='cloud/'+label+'/'+p.name
            z.writestr(name,exported_log(p,name,True))
        for build in sorted(fixture.glob('build*')):
            if not build.is_dir(): continue
            for p in sorted(build.iterdir()):
                if p.is_file() and p.suffix in ('.json','.log','.txt'):
                    name='cloud/'+label+'/'+build.name+'/'+p.name
                    z.writestr(name,exported_log(p,name))
        for p in sorted(fixture.glob('*residual*.json')):
            name='cloud/'+label+'/'+p.name
            z.writestr(name,exported_log(p,name))
        for directory in sorted(fixture.glob('attempt-*-failed')):
            for p in sorted(directory.glob('*.log')):
                name='cloud/'+label+'/'+directory.name+'/'+p.name
                z.writestr(name,exported_log(p,name,True))
            for p in directory.glob('*residual*.json'):
                name='cloud/'+label+'/'+directory.name+'/'+p.name
                z.writestr(name,exported_log(p,name))

# All journals are typed allowlists. No project account/private or public IP,
# terminal byte stream, directory entry list, path or File body is exported.
def journal(p):
    src=read(p)
    result={k:src.get(k) for k in ['plannedGenerations','labInstanceId','stage','hello','heartbeat','passed']}
    result['resources']=[{k:r[k] for k in ['resourceType','providerId','generation','logicalName','observedState'] if k in r} for r in src['resources']]
    typed_manifest.append({'source':str(p.relative_to(repo)), 'originalSha256':sha(p.read_bytes()),
        'export':'allowlisted journal metadata/resources only','originalPreserved':True})
    return result
for label,fixture in attempts:
    src=fixture/'ktcloud-control-ledger.json'
    if src.exists(): save(label+'-final-ledger.json',journal(src))
    for p in fixture.glob('attempt-*-failed/ktcloud-control-ledger.json'):
        save(label+'-original-failed-ledger.json',journal(p))
bootstrap=read(final/'ktcloud-bootstrap-ledger.json')
keys=['name','stage','imageId','imageName','flavorId','flavorName','vcpus','ramMiB','diskGiB','tierId','tierRefId','externalTierId',
      'keypairName','keyCreated','serverId','volumeIds','publicIpId','forwardId','policyIds','jobIds','deleted',
      'policyJobs','policyAttempted','publicIpCreateAttempted','forwardCreateAttempted']
save('bootstrap-owned-ledger.json',{k:bootstrap[k] for k in keys if k in bootstrap})
for name in ['baseline-inventory.json','final-inventory.json','ktcloud-residual-current.json']:
    save(name,read(final/name))

# Each final residual query must cover the original failed journal, including
# IDs already absent during deferred cleanup or first-run reconciliation.
all_lab_refs=set()
residual_reports=[]
for label,fixture in attempts:
    current=read(fixture/'ktcloud-control-ledger.json')
    refs={(r['resourceType'],r['providerId']) for r in current['resources']}
    for original in fixture.glob('attempt-*-failed/ktcloud-control-ledger.json'):
        original_refs={(r['resourceType'],r['providerId']) for r in read(original)['resources']}
        if not original_refs <= refs:
            raise SystemExit('Final residual does not cover original ownership: '+label)
    report=read(fixture/'ktcloud-residual-current.json')
    if report['ownedResidualTotal'] != 0:
        raise SystemExit('Previous/current fixture still has owned residuals: '+label)
    all_lab_refs.update(refs)
    residual_reports.append({'attempt':label,'queriedLabRefs':len(refs),
        'originalOwnershipCovered':True,'report':report})
# inventory selects existing DMZ/external tiers; it creates three policies,
# not a bootstrap tier. Shared tier IDs never enter owned resource counts.
original_baseline=read(final/'baseline-inventory.json')
if bootstrap['tierId'] not in original_baseline['TIER']:
    raise SystemExit('Selected bootstrap tier is outside the shared baseline')
bootstrap_refs={('SERVER',bootstrap['serverId']),('KEYPAIR',bootstrap['keypairName']),
    ('PUBLIC_IP',bootstrap['publicIpId']),('FORWARD',bootstrap['forwardId'])}
bootstrap_refs.update(('VOLUME',v) for v in bootstrap['volumeIds'])
bootstrap_refs.update(('KT_FIREWALL_POLICY',p) for p in bootstrap['policyIds'])
save('all-attempt-residuals.json',{'uniqueLabResources':len(all_lab_refs),
    'uniqueBootstrapResources':len(bootstrap_refs),'uniqueOwnedResources':len(all_lab_refs|bootstrap_refs),
    'allKnownIdsAbsent':True,'attempts':residual_reports,
    'bootstrapTierSharedBaselinePreserved':True,
    'scope':'exact typed IDs from each original/current journal; preserved seven-kind baseline checked separately'})

checks=[]
for folder in [auto_folder,'web-final','contracts-final']:
    for p in sorted((validation/folder).glob('*-run.json')):
        checks.append(dict(read(p),name=folder+'/'+p.stem))
if read(validation/auto_folder/'compiled-source-auto-before.json')['files'] != tested['files']:
    raise SystemExit('Final automatic and actual build sources differ')
if not read(validation/auto_folder/'auto-source-stability.json')['compiledSourceUnchanged']:
    raise SystemExit('Final automatic source changed during execution')
skips=[]
outputs={}
for line in (validation/auto_folder/'go-unit.log').read_text(encoding='utf-8').splitlines():
    try: e=json.loads(line)
    except ValueError: continue
    key=(e.get('Package'),e.get('Test'))
    if e.get('Test') and e.get('Action')=='output': outputs.setdefault(key,[]).append(e.get('Output',''))
    if e.get('Action')=='skip' and e.get('Test'):
        skips.append({'package':key[0],'test':key[1],'output':''.join(outputs.get(key,[])).strip()})
save('default-suite-skips.json',skips)
led=journal(final/'ktcloud-control-ledger.json')
res=read(final/'ktcloud-residual-current.json')
baseline=read(final/'final-inventory.json')
live_log=(final/'ktcloud-control-acceptance.log').read_text(encoding='utf-8-sig')
stages=re.findall(r'^Control evidence: (.+)$',live_log,re.M)
cloud_pass=(led['passed'] is True and res['ownedResidualTotal']==0 and baseline['baselinePreserved'] is True
    and baseline['deleted'] is True
    and 'REAL_PRODUCTION_CONNECTOR_WSS_LIFECYCLE_PASS_OWNED_RESIDUAL_0' in stages
    and 'OP02_PRODUCTION_CONNECTOR_GRACEFUL_SHUTDOWN_JOINED_PASS' in stages)
save('validation-summary.json',{'recordedAt':datetime.now(timezone.utc).isoformat(),
    'base':git('merge-base','HEAD','origin/main'),'testedHead':tested['head'],'branch':git('branch','--show-current'),
    'currentCompiledSourceMatchesTested':True,'automaticChecks':checks,'defaultUnitSkipCount':len(skips),
    'countSemantics':'Go PASS/SKIP events include parent tests and subtests; web 13 files / 170 cases',
    'actualCloudPassed':cloud_pass,'actualCloudStages':stages,'ownedResidualTotal':res['ownedResidualTotal'],
    'baselinePreserved':baseline['baselinePreserved'],'attempts':[{'number':1,'result':'FAILED: SSH identity output absent; cleanup UNKNOWN then observed recovery'},
    {'number':2,'result':'FAILED: production SSH Ready timeout; deferred owned cleanup succeeded'},
    {'number':3,'result':'FAILED: fixture incorrectly awaited duplicate completed File ACK; deferred owned cleanup succeeded'},
    {'number':4,'result':'FAILED: additional fixture pinned SSH unavailable after unknown Save PASS; deferred owned cleanup succeeded; exact SSH cause unconfirmed'},
    {'number':5,'result':'FAILED: fixture opened g2 Terminal after successful empty Cleanup invalidated access; both generations cleaned; production lifecycle was correct'},
    {'number':len(fixtures),'result':'PASS' if cloud_pass else 'FAILED/INCOMPLETE; read actual raw log'}],
    'boundary':'synthetic TLS SaaS peers; real production Connector, KT API/VM, pinned SSH, PTY and rooted SFTP; Browser/Backend C2 not executed'})
save('raw-log-manifest.json',manifest)
save('typed-export-manifest.json',typed_manifest)
save('publication-scan.json',{'allowlistedSourcesOnly':True,'secretPatternScan':'PASS for published raw log archive entries',
    'protectedInputsAndBinariesExcluded':True,'terminalFileBodiesNotPersistedByHarness':True,
    'ipv4Redactions':sum(m['ipv4Redactions'] for m in manifest),'scope':'all published raw entries; typed journals and human audit are separate',
    'archiveSha256':sha((out/'validation-logs.zip').read_bytes())})
print(json.dumps({'archiveBytes':(out/'validation-logs.zip').stat().st_size,'entries':len(manifest),
      'actualCloudPassed':cloud_pass,'ownedResidualTotal':res['ownedResidualTotal']}))
