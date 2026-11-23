# PR196 DB·공개 원본 보관·콜백 제한 시간 — 독립 최종 증분 검토

검토일: 2026-11-23 KST. 구현자와 분리된 reviewer가 PRE_MERGE_AUDIT.md를 다시 읽고 로컬 소스·계약·원본 로그·제출 Evidence를 검토했다. 제품/테스트/Evidence를 수정하지 않았으며 AWS·SQL·SSM·Secret·Slack·원격 API를 실행하지 않았다.

## 1. 고정 범위와 결론

- 저장소: labbit-app, 로컬 브랜치 `codex/feat/LBT-60-stage3-live-prep-20261123`.
- base/main: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- 검토 HEAD: `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1` + 미커밋 로컬 증분.
- 대상: 선택적 ArtifactRegion/DatabaseTunnelPort, DB transport/TLS, invocation 단위 immutable byte 준비 및 Resume/Restriction consumer, 공개 원본 보관 도구·fixture, Runtime0.1.15, CI/Makefile/TESTING, 후속 문서, `2026-11-23-db-artifact-validation` packet. 앞선 실제 Collector 경로/원상복귀 검토도 참조했다.
- 과거 base→HEAD PR387파일은 기존 독립 감사와 정확 HEAD QA를 재사용했다. 이번에 387파일 전체를 새로 감사하거나 과거 실측을 재실행한 것은 아니다. 외부 SSOT는 구현자가 새로 조회해 보존한 `ssot-reviewed.json` 및 기존 원문/코드와 대조했으며, reviewer가 Jira/Confluence를 다시 호출하지는 않았다.

**현재 로컬 증분의 검토 범위에서 추가로 확인된 차단 결함 없음. DBART-01/02 P2 및 DBART-03 문서 불일치는 해결·관련 회귀 확인. 판정은 코드·제출 Evidence 정합성의 PASS_SCOPED다. 실제 signed init/Pause/Resume, 전체 Worker 복구·DR GO 인수는 아니다. 최종 artifact-manifest 봉인 전 검토이므로 최종 manifest의 coverage/hash는 봉인 후 별도 확인해야 한다.**

감사 통과를 push/PR/merge/배포/메시지 전송 승인으로 해석하지 않는다. 현재 로컬 v0.1.15 증분은 앞서 게시한 be4d0ba/v0.1.14 후보 이미지에 포함되지 않았다.

## 2. 요구사항→구현→검증 추적

| 요구/경계 | 소스·consumer | 자동/제출 검증 | 실제 제출 Evidence와 한계 |
| --- | --- | --- | --- |
| 제어 리전과 S3 보관 리전 구분, 기존 Policy 호환 | contract.go, awscli.go; optional ArtifactRegion 없으면 기존 Region | 리전/잘못된 region/고정 참조 회귀; 이전 독립 검토 재사용 | 이전 고정 원본의 실제 Osaka readback. 이를 Writer/Fencing 증거로 해석하지 않음 |
| DB 목적지·TLS 신원 유지, TCP만 loopback으로 전달 | database.go:15 DatabaseConfig/54 VerifySQL; Policy DatabaseTunnelPort | 직접 연결/loopback/exact host·port/잘못된 TLS·DSN·환경 경계, local unit/race | db-product-tunnel.json의 제품 VerifySQL193ms/TLS1.3/readonly 확인. 5개 control table0행은 Single Writer 증거가 아님 |
| 인증·historical inspect 후 새 실행만 바이트 준비 | prepared.go:14, verified.go:104, restrict.go:62 | immutable reference/hash/size, 실패·취소·부분 준비 정리, historical0 I/O, 준비 중 경쟁·만료 회귀 | 실제 immutable 원본2개 준비3719ms. callback은 만료/SIMULATION 거부만 측정 |
| callback2초/CLI15초 유지, 현재 서명·대상·purpose·TTL 재검증 | PreparedArtifacts는 raw bytes만 보유; Verifier/Resumer/Restrictor 기존 검증과 inner Inspect/CAS 유지 | 2100ms 다운로드 후 callback, fake clock 만료/새 INTENT0, race receipt 재사용 차단 | callback523µs/타이머 분해능 내, 추가 I/O0. 정상 LIVE 전체 조건의 지연 증거는 아님 |
| 공개 원본1회 조건부 생성·불명 재전송 금지 | dr_control_artifact_store.py:122; exclusive lock/local INTENT/fsync/If-None-Match/AES256 | Fake12개 normal/-O: identity/window/versioning/동시실행/UNKNOWN/고정 receipt/실패 보존 | 실제 expired SIMULATION 2개만 생성. 초기 PUT 당시 소스 hash 미보존을 명시. 최종 소스로 PUT 재실행하지 않음 |
| 정확 VersionId 전체 크기/암호화·바이트 확인 | script:183 exact-version Head + ranged Get/원본 비교 | larger-object 회귀 및 exact VersionId/metadata 실패 | 최종 PUT 금지 Runner의 Head/Get2/2. 새 독립 Reader·RequestId는 미보존/미수행 |
| 계약·CI·운영 한계 정합 | runtime0.1.15 YAML/README/dr-control.md, artifact-storage 문서, go.yml/Makefile/TESTING | YAML parse·CI 연결 기록; stdlib Fake normal/-O | CI는 오프라인 fixture만 추가. AWS 권한·배포 단계 추가 없음. fresh GitHub/Linux 실행 미수행 명시 |

HTTP/WSS/Connector 공개 계약, DB migration, SaaS startup/rollout 동작을 새로 변경하지 않은 것을 해당 diff에서 확인했다. 별도 publisher는 workercontrol 명령을 호출하지 않는다.

## 3. 수정 전 findings와 해결

| ID | 원인/재현·영향 | 해결 및 재검증 |
| --- | --- | --- |
| DBART-01 / P2 | publisher가 initial window check 후 INTENT/body fsync 중 만료되면 PUT 직전 재검사가 없어 창 밖 PUT 가능. reviewer가 Mock 시각 이동으로 수정 전 재현·보고 | 현재 script160–165의 fsync 직후 재검사. `WINDOW_EXPIRED_BEFORE_PUT` INTENT 보존, PUT0. 만료/역행 경계 fixture 및 normal/-O PASS |
| DBART-02 / P2 | 원래 ranged Get의 ContentLength/바이트 일치만으로 더 큰 전체 객체의 size를 증명하지 못함. 구현자 수정 전 보고 | exact VersionId Head의 전체 ContentLength/AES256/DeleteMarker 검증 추가. larger-object fixture 포함12개 PASS. 최종 actual readback2/2는 수정 후 Head/Get만 별도 수행 |
| DBART-03 / P3 | owning Runtime YAML/README0.1.14와 후속 문서0.1.15 불일치 | YAML/README/dr-control.md0.1.15 정합 및 parser 확인. 현재 배포/이미지 source와 로컬 보완의 버전 차이 명시 |

직접 DB 경로 미준비와 기존 CLI reader의 2초 초과는 준비 환경/가용성 공백이며, Writer safety를 우회한 보안 결함으로 재분류하지 않았다.

## 4. 실패·보안·수명주기

- 준비 단계는 exact Reference의 크기·SHA를 확인한 바이트만 invocation 로컬로 복사한다. 권한·서명/TTL 판정·proof·journal은 캐시하지 않으며 callback에서 현재 시각으로 다시 검증한다. 부분 실패/종료 후 Clear와 historical/CAS 경로를 확인했다.
- 바이트·이미 읽은 Policy는 snapshot이다. 이후 IAM 회수/객체 삭제/Policy 수정은 같은 invocation에서 즉시 반영되지 않는다. 이 한계가 계약/운영 문서에 드러나 있으며 즉시 취소 기능으로 과대 주장하지 않는다.
- loopback은 고정 RDS Host/Port/DB/User·TLS ServerName/CA를 유지한 transport 교체다. 임의 대상/비검증 TLS/DSN options는 거절한다. SSM exact identity/EC2/document/remotehost·포트 독점·종료는 외부 coordinator의 운영 선행조건이다.
- publisher는 exclusive local lock과 durable INTENT를 먼저 남긴다. 응답 불명/VersionId 불명은 UNKNOWN/INTENT로 남기고 자동 PUT을 반복하지 않는다. STORED/READBACK receipt는 기록된 버전 Head/Get만 허용한다. 로컬 journal 유실·전원 손실 durability/부모 디렉터리 DACL/운영자 변조 방어는 문서의 신뢰 전제이며 분산 transaction 보장은 아니다.
- 저장 성공/shape 검사는 서명 진위 또는 실행 승인이 아니다. 만료 SIMULATION 샘플은 실행 권한으로 사용되지 않고 실제 Store/INTENT/init/Pause/Resume 생성이 없다.
- 12/24 보존 시각은 수동 검토 기준이다. ObjectLock·자동 lifecycle·자동 삭제가 설정됐다는 주장이 없다. 새 샘플2개1545B의 담당/비용 검토·독립 Reader 및 RequestId 공백을 기록했다.
- db-product-tunnel의 SSM 종료 field는 직접 API ACK 자체가 아니라 History=Terminated/Active 부재 등 상태 교차확인이라는 설명이 있다. 공유 Secret/서비스 설정을 바꾸지 않았다는 주장과 SSM세션/S3PUT 실제 호출을 구분한다.
- 실제 Fencing·Single Writer·Provider/UNKNOWN 작업 정산·전체 Pod coverage는 미검증이다. SQL0행/유휴 Worker/health/ReplicaLag를 이 조건들의 PASS로 확대하지 않는다.

## 5. 테스트·Evidence 독립 확인

reviewer가 이전 구현 검토 중 직접 실행한 cloud-free Go 영향3패키지 race 및 publisher stdlib normal/-O12개는 PASS였다. 이번 최종 문서 검토에서는 동일 제품소스의 대규모 테스트를 반복하지 않고 아래 제출 원본을 독립 파싱했다. 공유 DB/실 AWS를 reviewer가 실행한 것은 없다.

- raw Go JSON events: initial race310 PASS, final3pkg race210 PASS, 전체 Go4684 PASS/88 SKIP.
- 폐기용 PG16.15: 초기336 PASS/261 FAIL(role fixture 불일치) 보존, 수정된 로컬 fixture703 PASS. 운영/공유 DB integration 성공으로 바꾸지 않는다.
- Web Windows 초기492 PASS+EPERM1 실패 후 소스 불변 재실행493 PASS,35파일. 과거 Windows process/launcher/후행 psql 실패 기록도 보존·구분했다.
- Python stdlib Fake12 normal/12 optimized, vet/build/typecheck/lint/runtime parser 결과를 별도 scope로 확인했다. 새 원격 CI/Linux 재실행, 정상 LIVE proof 전체 callback, 실제 제어 mutation은 미실행이다.
- gzip5개 모두 압축 SHA·해제 후 원본 SHA/크기 일치. 로그를 요약으로 대체하거나 초기 실패를 삭제하지 않았다.
- source-manifest 최종21/21 raw/LF해시 일치. 최초23항목에 포함된 generated ignored pycache2개는 구현자가 generated_excluded로 분리했으며, 재검사에서 source 목록에 pyc가 없음을 확인했다.
- source-locations15개 anchor/line 일치. 공개 helper17개가 현재 local helper와 바이트 일치. sourceOnly/packet secret-scan 제출결과0건을 확인했으나 이를 모든 과거 로컬 파일의 무조건적인 scanner PASS로 확대하지 않는다.
- 이전 packet manifest4개의 SHA 참조 일치. 직전 sealed collector packet101항목은 safe path/size/SHA까지 다시 확인해 모두 불변이었다.
- 최종 새 artifact-manifest는 이 보고 시점 미봉인이다. 본 보고서를 복사한 뒤 coverage/hash 확인이 남는다.

## 6. 남은 운영 Gate

현재 증분으로 읽기 전용 DB 운송, 공개 샘플 보관/고정버전 readback, 부정 callback 제한 시간 경로를 보완했다. 실제 시험 전에는 배포 후보 source/키·Policy/진짜 승인/전체 Pod·부팅 정보·signed LIVE 수집, Provider·기수락/UNKNOWN 정산과 Fencing/SingleWriter 근거, 작업창·복귀·잔여 자원 기준을 갖춰야 한다. 실제 init/Pause/Resume와 다중 Pod claim/dispatch/drain/재개·장애 회복, Failover/Failback 및 RTO/RPO는 이 결과로 인수할 수 없다.

기존 ReplicaLag 정책 FAIL, 필수4 NOT_VERIFIED, execution_gate=HOLD, recovery_authorized=false, cloud_verified=false 유지가 타당하다. 이번 결과는 제한 scope의 코드·Evidence 검토이며 전체 DR 완료나 merge 승인 판정이 아니다.

