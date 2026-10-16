from pathlib import Path
import json,subprocess as sp,hashlib,datetime
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=ROOT/'docs/connector/evidence/LBT-174/2026-10-16-network-race'
def save(name,v):(DEST/name).write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
linux=json.loads((OUT/'linux-v3/linux-ci-summary.json').read_text(encoding='utf8'))
assert linux['gatePass'] and len(linux['observedParents'])==12 and not linux['dataRaceReports']
cleanup=[]
for name,directory in [('v1',OUT),('v2',OUT/'probe-v2'),('v3',OUT/'linux-v3')]:
 result=json.loads((directory/'run-result.json').read_text(encoding='utf8'))
 es=[json.loads(x) for x in (directory/'local-network-race.log').read_text(encoding='utf8').splitlines() if x.startswith('{')]
 outputs=[e.get('Output','') for e in es]
 marks=[s.strip() for s in outputs if any(t in s for t in ['exact tracked absence','Temporary Management security group GET absence','Temporary integration keypair GET absence'])]
 errors=[s for s in outputs if any(t in s for t in ['cleanup failed','resource remains after cleanup','absence confirmation failed'])]
 assert len(marks)==3 and not errors
 assert result['sourceBefore']==result['sourceAfter'] and result['sourceBefore']['mismatches']==0
 cleanup.append({'stage':name,'hostGoExit':result['exitCode'],'elapsedSeconds':result['elapsedSeconds'],'trackedResources':8,'markers':marks,'cleanupErrors':errors,'sourceBefore':result['sourceBefore'],'sourceAfter':result['sourceAfter'],'postgresResidualDBCount':(directory/'postgres-cleanup.log').read_text().strip() if (directory/'postgres-cleanup.log').exists() else None,'postgresQueryExit':result.get('postgresQueryExit'),'postgresStopExit':result.get('postgresStopExit')})
status=sp.run([r'C:\Users\user\.cache\labbit-lbt174-pg16\pgsql\bin\pg_ctl.exe','-D',r'C:\Users\user\.cache\labbit-lbt174-pg16\data','status'],capture_output=True,text=True)
assert status.returncode!=0 and 'no server running' in status.stdout+status.stderr
save('cleanup-observations.json',{'scope':'Only exact tracked resources and owned helper SG/keypair from these three runs. Not all user/cloud resources. Disposable swap/packages/routes removed with owned VM.','runs':cleanup,'finalPGStatusExit':status.returncode,'finalPGStatus':(status.stdout+status.stderr).strip()})
audit=(DEST/'AUDIT.md').read_text(encoding='utf8')
audit=audit.replace('**진행 중 기록 — 최종 판정은 실행·독립검토 후 갱신한다.**','**실행 집계 완료. native Linux 특수 race 검증은 충족했다. Internet 구성 가용성 한계는 미해결이며 무조건 PR 가능 또는 전체 LBT-174 완료로 판정하지 않는다.**')
audit=audit.replace('| v3 nativeLinux | 진행 중. A/B45초 timeout 때문에 hostparent는 실패 상태다. nativeCI의 실제 자식 결과·exact12 parents/skip/race는 별도 집계한다 |','| v3 nativeLinux | native CI 3명령 모두exit0, phase591.285초, C1 정확12parent/총67test PASS+packagePASS(6.132초), fail0/skip0/race0. sourcea7d502. hostgoexit1/945.985초는 별도 A/B45초timeout 때문이며 전체fixturePASS로 바꾸지 않음. tracked8+보조SG/keypair GET404, PGDB0/query0/stop0, pg_ctl status no-server 확인 |')
audit=audit.replace('Management선택시차단/Lab선택시성공','Management선택시timeout/Lab선택시성공')
audit=audit.replace('F174-N001 Medium, 기존 구성','F174-N001 (= reviewer F174-NET001) Medium, 기존 구성')
audit=audit.replace('| F174-NDOC001 Low, Evidence 표현 |','| F174-METHOD003 Low, 외부 재현 runner 소유권 | 원본 `run_validation.py:20–33`은 start실패해도 finally에서PG stop을 호출. 기존동일dataPG가 이미 실행중인 조건에서 이번run이시작하지않은PG를중단할수있음. 독립stub으로원본startreject→query1/stop1/test0 확인 | 사용자에 수정전 보고. 원본live/as-run은불변; 새 `run_validation_safe.py`는 start성공뒤에만 started_here를설정하고 그때만query/stop. 회귀결과는 독립검토fixed-runner JSON을따름. 현재v1/v3는직접start성공이므로피해관측없음 |\n| F174-NDOC001 Low, Evidence 표현 |')
audit=audit.replace('최종 판정은 pending이다.','최종 판정: **Linux native 검증은 충족했다. Internet 동일 safeerror 재현과 경로 A/B 조사는 수행했으나, 최초 역사적 단일 원인 확정은 자료 부족으로 불가하며 구성 가용성 보완이 필요하다**. [Linux 정확 집계](run/linux-v3/linux-ci-summary.json), [정리 관측](cleanup-observations.json), [최종 독립 검토](independent-review/FINAL_AUDIT.md), [패키지 검증](package-verification.json)을 근거로 삼는다. 전체53 Go package 검색에서Evidence package0, 새Go코드0과보호입력 exact일치0도확인한다.')
audit+='\n최종 raw는 [provenance](raw-provenance.json)의 원본/보관사본 hash와 길이로 식별한다. v1 apt traceback의 실제Labgateway만role표기로치환한사본이며원본은외부tmp에보존했다. 나머지Markdown링크의 .go.txt/.log.gz 보관형식변환도명시한다. 이전frozen275파일의byteSHA는전부동일하다. stage/commit/push/PR/merge/배포/외부메시지는없다.\n'
(DEST/'AUDIT.md').write_text(audit,encoding='utf8')
repro=(DEST/'REPRODUCE.md').read_text(encoding='utf8')
repro=repro.replace('`prepare_linux_v3.py` 후 `linux-v3/run_validation.py`는','`prepare_linux_v3.py` 후 `make_safe_runner.py`로 새 소유권 보호 runner를 생성하고 `linux-v3/run_validation_safe.py`를 실행한다. 원래 실제 실행 runner `linux-v3/run_validation.py`는 역사적 원본으로 보존한다. nativeLinux runner는')
repro+='\n재현도구 소유권 보완: 원본runner는 PGstart실패에도finally stop을호출하는F174-METHOD003가있다. 원본실제실행은둘다PGstart성공이었으며최종DB0/정지확인했다. 향후재현은새safeRunner만사용한다. 이는실행하지않은새제품검증이아니며무해한subprocessstub으로startreject/normal/Go실패/timeout의정리소유권을검증한다.\n'
(DEST/'REPRODUCE.md').write_text(repro,encoding='utf8')
save('final-state.json',{'recordedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'branch':sp.check_output(['git','branch','--show-current'],cwd=ROOT).decode().strip(),'head':sp.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip(),'stagedFileCount':len(sp.check_output(['git','diff','--cached','--name-only'],cwd=ROOT).decode().splitlines()),'goSourceChangedThisRequest':False,'sourceSHA256':cleanup[-1]['sourceAfter']['sourceSHA256'],'nativeLinuxC1Gate':True,'hostDiagnosticWrapperExit':cleanup[-1]['hostGoExit'],'internetAvailabilityLimitationUnresolved':True,'originalHistoricalCauseConfirmed':False,'pushOrPRCreated':False})
print(json.dumps({'nativeLinuxC1Gate':True,'hostGoExit':cleanup[-1]['hostGoExit'],'cleanupRuns':len(cleanup),'trackedResourcesPerRun':8,'finalPGStatusExit':status.returncode}),flush=True)
