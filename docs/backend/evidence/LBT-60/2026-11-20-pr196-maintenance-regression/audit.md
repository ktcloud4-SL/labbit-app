# PR196 공통 maintenance 회귀 검증 감사

2026-11-20 KST. 사용자 PRE_MERGE_AUDIT 및 저장소 AGENTS/CONTRIBUTING/TESTING 적용.

## 범위·버전

- 저장소 `ktcloud4-SL/labbit-app`, 기존 Draft [PR196](https://github.com/ktcloud4-SL/labbit-app/pull/196).
- base `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, 작업 전 HEAD `93002098d9867a0c00c0e3b39952dd4e290c6603`.
- 이번에는 제품 코드·migration·배포 설정을 추가 변경하지 않고, 기존 production 조립을 사용하는 통합 테스트와 계획을 추가했다. #194 제품 로직을 중복 구현하지 않는다.
- 누적 base→HEAD 및 local delta 전체 파일·목적·SHA256은 validation.json의 inventory에 기록한다. 기존 변경은 이전 `2026-11-20-pr196-validation` 감사와 추적하고, 이번 달라진 파일을 독립 재검토했다. 누적 변경 중 제외 파일은 없다. 과거 Evidence는 과거 이력으로 검토하며 이번 실행의 증거로 바꾸지 않는다.
- 미커밋 실행 소스는 inventory의 raw/LF SHA256으로 고정한다. 이 디렉터리 자체는 자기참조 hash에서 제외한다. stat상 dirty지만 HEAD 대비 내용이 같은 기존 8개 파일은 stage·수정하지 않는다.
- 개인 로컬 로그는 validation의 경로·SHA256으로 식별한다. Git 줄바꿈 변환 가능성을 검토하고 최종 focused race 원본·LF 보관본·staged blob의 동일 SHA256·크기를 확인했다. 실제 원본은 이미 LF라 바이트 변경이 없었다. Go 1.27.1, Windows, PostgreSQL 16.15 loopback 전용 폐기 서버를 사용했다. 로컬 보조 도구는 제품 런타임 의존성이 아니다.

## 최신 SSOT·추적성

PR196 리뷰, LBT-205/59/60 최신 본문·댓글, Confluence 검증 설계 9666637과 D-24 6815749, 저장소 지침, HTTP/runtime/DB/Connector의 적용 경계를 대조했다. LBT-59 #11258의 배포 인계와 #11261의 과거 종료 요청을 이번 운영 변경 승인으로 바꾸지 않는다. Terminal/Live·SSH·SFTP wire는 이번 증분 변경이 없으며 원문 비저장과 기존 연결 종료 규칙은 이전 누적 감사 및 그대로인 코드로 대조했다. Infra D01–D06/C01–C20은 이전 감사의 버전 고정된 출처를 사용하며 새 확정 결정을 만들지 않는다.

| 요구사항·근거 | 구현·consumer | 자동 테스트·실행 | 실환경 Evidence·공백 |
| --- | --- | --- | --- |
| LBT-205/#194·OpenAPI: 신규 3종 closed503, 현재 권한과 committed replay 우선 | 기존 Service/HTTP/maintenance; 새 httpapi test:17 | 3종 replay2회 receipt 동일·Location없음·전체 작업 row 불변, 401/403·회원권한 박탈·신규503, Provision409 | 폐기 PG·in-process HTTP 실제 실행. 서울 Pod/Browser/실사용 세션 미호출 |
| Runtime maintenance: 새 claim 차단, 기수락 실행은 완료까지 drain | 기존 gate/provision Executor; 새 app test:157 | 3종×local/durable ACTIVE, 두 stack의 closed claim0·전체 item row 불변, 같은 backlog open positive control, 실제 ACK 영속→Mock RESULT SUCCEEDED·잔여 작업 정확히 처리 | 실제 PG/loopback HTTP·WSS/Connector Supervisor, Provider Mock. 두 stack은 한 Go process이며 실제 N=2 Pod·파일 전파·Valkey 검증 아님 |
| Runtime durable: claim/prepare/dispatch exact binding·PAUSE_NEW | workercontrol/provision/router/postgres; 새 app test:329 | 실제 claim commit 응답 보류→peer Pause→dispatch 거부, RUNNING/lease 보존·자동 requeue0 | 단일 loopback DB Writer. 리전 간 Writer/Fencing·실제 Provider mutation 차단은 미검증 |
| 짧은 boundary0≠전송 작업0, UNKNOWN 보존 | 새 app test:386; 기존 result consumer | 실제 Report.Drained 이후 worker count1 유지, Mock UNKNOWN을 RECONCILING으로 기록, file open만으로 재실행0 | actual external command·VM 정산 미검증 |
| Runtime approved_resume: INTENT/APPLIED·신규 binding과 명시적 open | 실제 PG journal+MaintenanceResumer; 새 app test:455 | request에 고정된 합성 승인, 실제 INTENT/APPLIED, 새 production stack closed0/open2, old binding거부, 역사적 receipt 조회 비변경 | trusted 운영 Authorizer/Rechecker·조작 진입점은 미구현. 합성 PASS proof는 Fencing 증거 아님 |
| stale/동시성/승인 만료·target mismatch·응답유실 UNKNOWN | 기존 workercontrol/provision/connector 및 postgres tests | 관련 5pkg race와 실제 PG WorkerControl race 재실행, 이전 누적 DB/WSS/계약 검증과 연결 | 실제 Pod restart·TCP COMMIT 유실·shared DB 적용은 이전과 같이 미검증 |
| DR 결과 범위와 운영 안전성 | 공통 계획 M194/M196·dr-mutation-admission | 새 RUN/Source/환경 분리, 실제 후보 image/schema/관측/rollback·전체 Pod barrier 사전조건 문서화 | ReplicaLag FAIL·필수4 NOT_VERIFIED·HOLD·recovery_authorized=false·cloud_verified=false 유지 |

상세 행별 기대 결과와 재현 명령은 [공통 시험 계획](../../../dr-maintenance-integration.md)의 M194-01~06/M196-01~04를 따른다. 새 CI workflow나 시험용 운영 endpoint는 만들지 않는다.

## 수정 전에 보고한 findings

| ID·심각도 | 수정 전 위치·조건·영향 | 처리·회귀 |
| --- | --- | --- |
| DOC-INTEGRATION-01 / P3 | dr-mutation-admission:26/54의 실제 PG 전부 미실행·compile-only 표현이 9300209 실행 근거와 충돌 | 과거 감사는 보존하고 현재 가이드에서 폐기 PG 실행·공유 DB 미검증·공통 계획을 분리 |
| INTEGRATION-REVIEW-01 / P2 | 새 app test 이전:218–225. Provider 진입 직후 비동기 ACK가 DB 저장되기 전에 full row 비교하면 정상 stage 변경을 신규 claim으로 오인 | 정확한 in-flight instance와 kind별 ACK stage/미완료를 확인한 뒤 비교(:260). 최종 3회 race PASS |
| INTEGRATION-LIFECYCLE-01 / P3 | 새 app test 이전:205/306/371. 중간 Fatal에서 RunOne goroutine join 없이 DB cleanup으로 갈 수 있음 | drStartWorker(:124)의 cancel+unblock+bounded join, goroutine 내 Fatal 제거. 정상 경로 실제 실행, 강제 Fatal 전체 시나리오 별도 주입은 미실행 |
| EVIDENCE-PATH-01 / P3 | 기존 2026-11-20-maintenance-integration 경로는 과거 audit가 이미 점유 | 새 pr196-maintenance-regression 디렉터리와 양쪽 가이드 참조를 사용. 과거 기록 변경0 |

추가 관찰: Git autocrlf에 따른 로그 해시 변경 가능성을 점검했다. 독립 byte 대조 결과 원본은 이미 LF였고 원본·보관본·Git blob의 SHA256이 같으므로, 실제 해시 불일치 finding으로 분류하지 않는다.

새 테스트 작성 중 unused import compile 오류와 time.Time 구조체 `!=` 비교에 의한 receipt assertion 실패가 있었다. 후자는 UTC/Local 표현 차이이며 `.Equal`로 같은 instant를 비교하도록 수정했다. 최초 도구 출력은 있었지만 별도 원문 파일은 보존되지 않았으므로 재현 Evidence로 주장하지 않는다. 제품 오류로 해석하거나 최초부터 모든 시도가 PASS였다고 기록하지 않는다.

## 실행·skip·소스 경계

- 전체 App+HTTP integration: **1072 PASS 이벤트 / FAIL0 / SKIP10**. ACK/join 보완 전 실행이며 production/helper/HTTP는 최종과 동일하다. 이 실행을 최종 App 새 파일의 동일 SHA 검증으로 표시하지 않는다.
- 보완 후 새 App/HTTP 전체 `^TestDRMaintenance` 3회 race: **42 PASS 이벤트 / FAIL0 / SKIP0**. 14개 고유 test/subtest 결과를 3회 반복한 수치다. 원문 `focused-race.jsonl`에 최종 소스 결과를 보관했다.
- 관련 maintenance/workercontrol/provision/operation/connector 5pkg race: **531 PASS 이벤트 / FAIL0 / SKIP0**.
- 실제 PostgreSQL `^TestWorkerControl` race: **12 PASS 이벤트 / FAIL0 / SKIP0**. 최종 두 integration package go vet도 PASS.
- App skip10은 dump helper1 + Valkey fixture 부재9이며 validation에 이름을 기록한다. 새 테스트는 skip되지 않았다. 이 skip을 실제 다중 Pod PASS로 바꾸지 않는다.
- 전체 기본 Go/Web의 Windows 한계와 최초 실패는 이전 `2026-11-20-pr196-validation` 이력을 보존한다. 이번에 동일 전체 기본 검사를 재실행했다고 주장하지 않는다. 원격 최종 commit CI는 push 후 별도로 확인하며 과거 9300209 CI를 새 파일 검증으로 사용하지 않는다.

## 실패·보안·수명주기

- 신원·권한·멱등 의미를 API 원문과 DB row 양쪽으로 확인하며 original key로 현재 권한을 우회하지 못한다. 합성 UUID/label만 사용하고 비밀번호·운영 DSN·Cookie를 기록하지 않는다.
- 처음부터 nonempty backlog와 open positive control을 사용해 유휴 상태·연결 부재가 차단 성공을 가리지 않게 한다. 두 번째 stack은 closed RunOne의 false/nil까지 요구하므로 claim 후 route unavailable을 성공으로 오인하지 않는다.
- 미해결 committed claim·UNKNOWN의 lease/status/correlation을 보존한다. #194 graceful drain은 durable ACTIVE로 검증하고 #196 hard Pause와 분리한다. file open은 old binding이나 UNKNOWN 재전송 권한이 아니다.
- Provider는 채널로 실행 시점을 제어하며 고정 sleep으로 경합을 숨기지 않는다. 신규 fixture lease는 1분으로 두어 무관한 lease renew가 row 비교를 흔들지 않게 한다.
- Worker 취소·unblock·join → Connector/stack 종료 → pool close → 시험별 DB drop 순서를 코드에서 확인했다. 테스트 DB 잔여0·자체 PostgreSQL 서버 종료·port16596 listener0을 실제 확인했다. 기존 임시 바이너리/data 폴더는 로컬 보존하며 이번 삭제를 시도하지 않았다.
- 새 운영 권한/endpoint/secret/network/cloud 리소스를 만들지 않았다. 후보 배포는 image digest·migration12~14·trusted control adapter·전체 Pod·backup/rollback·작업창 선행조건이 있다. 불명 상태에서 자동 open/DROP/재요청하지 않는다.

## 독립 검토·판정

구현과 분리된 reviewer `/root/review_worker_resume`가 최신 Jira/Confluence·누적 경계와 새 코드·실제 ACK 순서·stack cleanup·공통 계획을 검토했다. reviewer는 수정 후 compile-only 2pkg를 직접 실행했으며, 실제 PG 테스트는 parent가 실행했다. ACK 경쟁·cleanup·Evidence 경로 지적을 수용한 수정이 해결됐음을 독립 확인했다. 누적 inventory67개와 실행 로그5개의 size·raw/LF SHA·결과 집계를 교차 확인했고 missing/extra0이며 이번 증분의 추가 차단 결함은 발견하지 못했다. reviewer 직접 실환경 실행으로 표현하지 않는다.

**이번 통합 테스트·계획 증분은 Draft 검토 범위다. 전체 PR의 merge·배포 가능 판정은 유보한다.** 실제 서울/공유 DB/전체 Pod/Provider 시험은 미실행이고, 운영 승인·Fencing Adapter와 후보 배포 준비가 남아 있다. 시험 계획이 있는 것과 실제 시험 완료는 다르다. 기존 DR HOLD를 해제하지 않으며 사용자 대신 팀 메시지를 전송하지 않는다.
