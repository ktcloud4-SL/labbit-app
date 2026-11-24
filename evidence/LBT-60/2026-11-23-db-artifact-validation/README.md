# DB·승인 원본·2초 검증 연결 — 2026-11-23

LBT-60 / PR196의 **로컬 미커밋 후속 보완**이다. 기준 main `753eecb`, PR HEAD `be4d0ba`.
실제 운영 Pause/Resume 완료나 전체 DR 인수 문서가 아니다. push/merge와 후보 재배포는 하지 않았다.
상세 계약은 [Runtime 0.1.15](../../../runtime/dr-control.md),
[재현 절차](../../../docs/backend/dr-control-artifact-storage.md), [감사 기록](AUDIT.md)을 따른다.

## 이번 실행

| 요구 | 코드·자동 검증 | 실제 관측·한계 |
| --- | --- | --- |
| 기존 SSM으로 제품 DB 연결 | `DatabaseTunnelPort`는 literal127.0.0.1 운송만 변경, 실제 RDS DSN/host/TLS 불변. foreign host/network·취소·TLS생략 부정 테스트 | `db-product-tunnel.json`: 실제 제품 DatabaseConfig/VerifySQL 193ms, TLS1.3, read-only schema/inventory 후 ROLLBACK, 제어5테이블0. Single Writer 증명 아님 |
| 공개 승인 원본 보관 | 별도 `scripts/dr_control_artifact_store.py`, INTENT/fsync→창 재검사→조건부 PUT1회→VersionId→Head/Get. Fake12 normal/-O | `storage-live-trial.json`: 만료 SIMULATION2건, 471+1074B, 고정 버전/AES256/원본 일치. 동일 호출은 PUT0 재조회만. 운영 승인으로 쓰지 않음 |
| 2초 제한 유지 | 인증/역사적 조회 뒤 invocation별8초 byte preload, signature/request/현재 TTL은 callback2초에서 재검증, inner Inspect/CAS 유지 | `actual-deadline-callback.json`: 실제 S3 준비3719ms, 승인523µs, 관측은 timer분해능 내, callback외부호출0, 만료 승인·SIMULATION 거부. 정상 LIVE 안전조건 전체 경로 시간 실측은 아님 |
| 기존 서비스 유지 | 서버/DB migration/배포·IAM·SG·NP 변경 없음 | `final-seoul-baseline.json`: 기존 이미지/N2 Ready/Argo Synced·Healthy·원래 Deployment/Argo/NP/SG 일치 |

실제 AWS 작업은 서울 SSM **임시 세션 Start/Terminate와 읽기 전용 DB 조회**, 오사카 S3 새 샘플2건
조건부 생성·지정 버전 조회다. “AWS 변경 전혀 없음”으로 해석하지 않는다. 서울 **서비스 설정** 변경0이다.
SSM exact 세션 History=Terminated·Active부재·프로세스 wait·로컬 포트 닫힘을 확인했다.
원본 `ssm_termination_acknowledged=true`는 이 **종료 상태 교차 확인**을 뜻하며 API 응답의 SessionId를
직접 대조했다는 의미가 아니다. `terminate_api_succeeded`와 종료 상태 근거를 구분한다.

## 원본·보존·추적

버킷은 기존 `labbit-osaka-dr-evidence-299311846289-trial01`, 별도 경로는
`dr-precheck/storage-trial/s2b-20261113-01/control-originals/pr196-20261123-0615/`다.
기존 조건부 생성 정책이 적용되며 IAM/bucket 정책·기존 key·Slack Sender 권한은 바꾸지 않았다.
`storage-plan.*.json`과 `storage-result.*.json`에 account/actor/UTC창·VersionId·SHA·크기·AES256이 있다.
`receipt-directory-acl.json`: 로컬 영속 receipt 디렉터리는 owner/SYSTEM/Administrators만 허용했다.
도구가 OS ACL을 자동 검증하지 않으므로 운영자가 실행 전에 확인해야 한다.

샘플은 이미 만료됐고 Observation Source=SIMULATION, 전 안전조건 NOT_VERIFIED다.
무작위 샘플 개인키는 RAM에서만 사용 후 폐기했다. 공개 Signed **정확 바이트**만 보관하며 운영 승인이나
LIVE 안전근거를 만들지 않았다. 이번 시험에서 실제 제어 Store·INTENT·init/Pause/Resume는 만들지 않았다.
새 객체의 독립 Reader 검증은 하지 않았고 S3 RequestId도 AWS CLI 결과에 보존하지 않았다.

수동 보존 검토는 `2026-12-24T06:17:35.032904Z`, 담당 서빈/LBT-60이다. 자동 삭제·Lifecycle 변경·
Object Lock은 설정하지 않았다. 명시된 날짜는 수동 관리 목표이며 관리자 삭제 방지 보장은 아니다.
보존·사용처·QA를 확인한 뒤 별도 결정한다. 기존2B 비용 관리와 함께 소량2개 실비용을 재검토한다.

최초 PUT/재조회는 **Head 전체크기 검사 추가 전** source로 실행됐다. PUT·INTENT 분기는 이후 그대로이며,
추가한 전체크기 검사는 `final-storage-readback.json`에서 최종 소스+PUT 금지 Runner로 Head/Get2건을
검증했다. 최초 실행 source의 당시 별도 해시 스냅샷은 남기지 못했으며 이를 최종 소스의 PUT 재실행으로
바꾸어 기록하지 않는다. 최종 source는 `source-manifest.json`; 실제 command와 범위는 `validation.json`이다.

## 테스트와 재현

Go race4패키지310PASS, 최종3패키지210PASS, 전체Go4684PASS/88SKIP,
영향범위3패키지 PostgreSQL16.15 통합703PASS, Web493PASS. Python12 Mock는 normal/-O 모두 통과했다.
Go vet/build, Web typecheck/lint/build, YAML/runtime/CI 연결 검사도 통과했다.
SKIP는 주로 실제 Provider/환경별 opt-in 조건이며 `validation.json`과 원본 로그에서 사유를 확인한다.
CI에는 새 Python fixture를 연결했지만 **이 로컬 미푸시 소스로 GitHub CI를 실행한 것은 아니다**.
Windows CRLF checkout의 형식은 canonical LF 비교와 신규 Go파일의 gofmt로 확인했다.
기존 설치 의존성을 사용해 Make target에 대응하는 명령을 직접 실행했으며 setup/npm ci는 반복하지 않았다.

초기 실패를 숨기지 않았다.

- 과거 로컬 PG fixture에 `postgres` 역할이 없어 첫 통합 실행 실패. 기존 fixture는 종료하고 별도
  loopback55438/`pr196_fixture`로 새 PG16을 준비한 뒤 통과. 공유 AWS DB를 시험 DB로 쓰지 않았다.
- 첫 Web은 임시 디렉터리 삭제 EPERM 1건/492PASS. source 변경 없이 전체 재실행493PASS.
- PG 시작 명령의 Windows 파이프가 서버 종료까지 대기하여 후행 psql은 종료 후 실패했다.
  독립 `pg-fixture-identity-final.log`와 실제 통합 결과로 기동 중 신원을 확인했다.
- 초기 helper compile의 미정의 region과 Python 테스트의 unittest.run 이름 충돌, validator 경로 설정 오류는
  시험 harness 단계에서 교정했다. 외부 mutation 전에 실패했으며 제품 결함/실환경 PASS로 세지 않았다.

`*-checks.json`은 실제 command/exit를 기록한다. 큰 JSON 로그는 gzip으로 정확 바이트를 보존했다.
`log-compression.json`의 원본 SHA/크기를 통해 해제 후 검증할 수 있다. `helpers/`의 `.txt`는 소스 스냅샷이며
자동 실행 파일이 아니다. 필요한 파일만 별도 `.local`에 복원해 검토 후 사용한다. 실제 AWS helper에는
고정 target/window가 있으므로 **새 승인·fresh identity·대상/세션 확인 없이 재실행하지 않는다**.
source/go module과 기존 도구·공식 CA·SSM plugin이 필요하며 비밀번호/개인키/인증 캐시는 포함하지 않았다.

로컬 PG55437/55438 종료·잔여 listener0은 `local-fixture-cleanup.json`에 있다. 디스크 fixture는 검토용으로
남았으며 AWS 비용 자원이 아니다. 앞선 sealed101개 collector packet은 수정하지 않았다.

## 남은 경계

바이트/Policy는 invocation snapshot이다. 다운로드 이후 IAM 회수/객체 삭제·파일 변경을 즉시 재조회하지 않는다.
신원/TTL/서명 **판정**이나 증거 PASS는 캐시하지 않으며 다른 invocation으로 바이트를 재사용하지 않는다.
이 한계를 수용할 수 없는 작업은 진행하지 않는다. 긴급 중단은 프로세스/DB 접근 통제와 journal 확인이 필요하다.

실제 운영 키/Policy·안전 관측·전체 Pod 계획·신규 control mode 배포·다중 Pod drain·UNKNOWN/Provider 정산,
Writer Fencing/Single Writer·승인 Pause/Resume·복귀 조건·Failover/Failback은 여전히 별도다.
기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false 유지.
