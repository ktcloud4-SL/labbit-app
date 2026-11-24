from pathlib import Path
import json, hashlib, subprocess, datetime, shutil, gzip
ROOT=Path(__file__).resolve().parent.parent
OUT=ROOT/'evidence/LBT-60/2026-11-23-operator-linkage-offline'
def sha(b):return hashlib.sha256(b).hexdigest()
def write(name,obj):(OUT/name).write_text(json.dumps(obj,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
source=json.loads((OUT/'source-manifest.json').read_text(encoding='utf-8'))
drift=[x['path'] for x in source['files'] if not x.get('deleted') and sha((ROOT/x['path']).read_bytes())!=x['sha256']]
review=json.loads((OUT/'pr196-linkage-review-source-final-20261123.json').read_text(encoding='utf-8-sig'))
reviewdrift=[x['path'] for x in review if sha((ROOT/x['path']).read_bytes())!=x['sha256']]
assert not drift and not reviewdrift,(drift,reviewdrift)
summary=json.loads((OUT/'test-summary.json').read_text(encoding='utf-8'))
for name in ['linkage-go-all-v5.jsonl','linkage-focused-producer.jsonl','linkage-pg-final.jsonl','linkage-pg-v5.jsonl']:
 assert summary['go'][name]['test_events'].get('fail',0)==0
 assert summary['go'][name]['package_events'].get('fail',0)==0
assert summary['go']['linkage-go-all-v5.jsonl']['test_events']['pass']==4604
assert summary['go']['linkage-pg-final.jsonl']['test_events']['pass']==1761
assert summary['linux_no_nic']=={'pass':124}
for name in ['gitleaks-source.json','gitleaks-raw-logs.json']: assert json.loads((OUT/name).read_text(encoding='utf-8-sig'))==[]
assert (ROOT/'.local/linkage-pg-cleanup.txt').read_text(encoding='utf-8-sig').splitlines()[0]=='0'
assert 'server stopped' in (ROOT/'.local/linkage-pg-cleanup.txt').read_text(encoding='utf-8-sig')
assert '@@LINKAGE_RESULT:0@@' in (ROOT/'.local/linkage-linux-serial.log').read_text(errors='replace')
assert '493 passed' in (ROOT/'.local/linkage-web-test-serial.txt').read_text(encoding='utf-8-sig')
assert json.loads((OUT/'migration-preservation.json').read_text())['existing_sql_unchanged']
for name in ['pr196-linkage-review-probe-20261123.go','pr196-linkage-review-counter-probe-20261123.go']:
 shutil.copyfile(Path(r'C:\Users\user\.codex\tmp')/name,OUT/(name+'.txt'))
shutil.copyfile(ROOT/'.local/finalize_linkage.py',OUT/'finalize_linkage.py')
shutil.copyfile(Path(__file__),OUT/'validate_linkage_packet.py')
commands=[
 {'command':'go test -count=1 -json ./...','log':'linkage-go-all-v5.jsonl.gz','exit':0,'scope':'Windows mock/unit; product source same; subsequent test-only producer assertion covered by focused and Linux runs'},
 {'command':'go test -count=1 -race -json ./internal/drcontrol ./internal/server/app ./internal/server/maintenance ./internal/server/workercontrol ./cmd/labbit-dr-control ./cmd/labbit-dr-evidence','log':'linkage-focused-producer.jsonl.gz','exit':0},
 {'command':'go test -count=1 -race -tags=integration -json ./internal/drcontrol ./internal/postgres ./internal/postgres/postgrestest ./internal/server/app ./internal/server/auth ./internal/server/httpapi','log':'linkage-pg-final.jsonl.gz','exit':0,'environment':'LABBIT_TEST_DATABASE_DSN=dedicated loopback PG16 fixture; no shared DB'},
 {'command':'go test -count=1 -race -tags=integration -json ./internal/drcontrol ./internal/server/app -run TestRestriction|TestSignedOperatorWithDisposablePostgres|TestAdminDRReadback','log':'linkage-pg-v5.jsonl.gz','exit':0},
 {'command':'go vet ./...; go build ./... (separate invocations)','logs':['linkage-vet-v5.txt.gz','linkage-build-v5.txt.gz'],'exit':0},
 {'command':'GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/drcontrol; offline noNIC QEMU harness','logs':['linkage-linux-compile.txt.gz','linkage-linux-serial.log.gz'],'exit':0,'race':False},
 {'command':'npm ci; npm run typecheck; npm run lint; npm run build (separate invocations)','exit':0},
 {'command':'npm run test -- --maxWorkers=1','log':'linkage-web-test-serial.txt.gz','exit':0,'earlier_failures':'parallel attempts EPERM retained separately'},
 {'command':'runtime YAML/migration metadata parse; gofmt comparison; git diff --check','exit':0},
 {'command':'gitleaks dir changed-source-export; gitleaks stdin raw logs (redact)','exit':0},
]
write('commands.json',commands)
write('validation.json',{'status':'PASS_SCOPED_LOCAL_IMPLEMENTATION','validated_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_files_verified':len(source['files']),'source_drift':drift,'independent_source_files_verified':len(review),'independent_source_drift':reviewdrift,'existing_sql1to14_unchanged':True,'local_pg_residual_test_databases':0,'local_pg_stopped':True,'linux_no_nic_poweroff':True,'actual_aws_calls':0,'actual_shared_database_calls':0,'push':False,'merge':False,'deployment':False,'slack_transmission':False,'execution_gate':'HOLD','recovery_authorized':False,'cloud_verified':False,'retained_dr_fail':'ReplicaLag policy','retained_not_verified_gates':4,'new_product_blocking_findings':[],'operational_acceptance':False})
files=[]
for p in sorted(OUT.iterdir()):
 if p.is_file() and p.name!='artifact-manifest.json':files.append({'file':p.name,'bytes':p.stat().st_size,'sha256':sha(p.read_bytes())})
write('artifact-manifest.json',{'source_manifest_sha256':sha((OUT/'source-manifest.json').read_bytes()),'self_excluded':True,'files':files})
for x in files:
 p=OUT/x['file'];assert p.stat().st_size==x['bytes'] and sha(p.read_bytes())==x['sha256']
print(json.dumps({'status':'PASS_SCOPED_LOCAL_IMPLEMENTATION','source':len(source['files']),'review':len(review),'artifacts':len(files),'drift':0}))
