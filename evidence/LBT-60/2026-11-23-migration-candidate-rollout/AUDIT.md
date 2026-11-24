# Migration / candidate compatibility audit

2026-11-23 KST. `PRE_MERGE_AUDIT.md`를 작업 시작에 전체 읽었다.
검토 대상 app base/main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`.
작업 브랜치 `codex/feat/LBT-60-stage3-live-prep-20261123`. 제품 소스·dependency·migration 원문은 이번에 수정하지 않았다.
PR196 Draft/Open/미병합을 native GitHub 조회로 재확인했다. 새 push·merge·Slack/외부 메시지 없음.

## 범위와 최신 근거

- 사용자: 서울 이용자 없음, 다음 단계 실행, migration/후보배포 전에 변경/복귀 공지, merge 금지.
  실제 변경 전 snapshot 비용·additive DDL·Argo 자동 Sync 지연·Pod 교체·legacy 모드·복귀 경계를 채팅에 공지했다.
- Jira LBT-60 description과 최신 댓글 #11265–11267를 직접 재조회했다. 최신 #11267은 be4d0ba P2 코드/시험 해소이며 DR GO가 아니다.
- [D-24 v13](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6815749) 재조회: 실제 Provider 대조, UNKNOWN 자동 Mutation 재실행 금지,
  운영자 판단과 Warm Standby 전략·실제 Failover/Failback·RTO/RPO 경계를 적용한다. 이번 제한 기동을 전체 DR 인수로 바꾸지 않는다.
- 저장소 AGENTS/CONTRIBUTING/TESTING, Runtime worker-control/maintenance 계약, migration12–15와 runner/CheckSchema,
  DR 통합/배포 준비문서, producer/consumer 설정 및 기존 exact-source/후보 provenance 감사를 대조했다.
- HTTP/WSS/Reconciliation 계약은 이번에 수정하지 않았다. 사용자 요청/실제 Provider 작업을 만들지 않았으므로 전체 기능 AC는 미실행이다.
- 이전 전체 PR 감사는 같은 be4d0ba의 코드/CI에 한해 참조한다. 387개 원격 PR 변경 전체를 이번에 새로 독립 재실행/리뷰했다고 주장하지 않는다.
  새 검토 범위는 one-window 실행 coordinator·readonly helper·단계별 live Evidence·문서 후속이다.

## 전체 로컬 변경

`whole-local-status.txt`는 tracked·untracked 전체 목록, `local-docs.diff`는 두 tracked 문서 diff다.
새 시험 packet 전체, `dr-pr196-trial-preparation.md`, 기존 두 문서의 실제 적용 후 시점 구분을 검토했다.
이전 두 packet은 `prior-artifact-integrity.json`에서96개 엔트리를 대조했다: 실제 과거 자료93개 불변, 후속 docs3개 의도적 갱신.
기존 manifest·과거 실패 로그를 덮어쓰지 않았다. 새 manifest에 이번 파일 bytes/hash를 별도로 기록한다.

ignored `.local` coordinator/readonly helper·테스트는 실행 의존성이므로 `source/*.txt`로 보존했다.
이름/경로가 현재 회차에 고정된 one-off 도구로, 범용 DR 실행기/배포 제품 API가 아니다.
보호 디렉터리의 키·Secret DSN 원문은 공개 검토 대상에 복사하지 않았다. 이번 migration에서 개인키를 사용하지 않았다.
모듈·제품 source가 미변경인 점과 새 helper의 hash를 구분한다.

## 수정 전 findings 및 처리

| ID·분류 | 파일/라인·재현 | 영향 및 수정·검증 |
| --- | --- | --- |
| ROLL-01/P2, 실행 전 보고·해소 | be4d0ba `internal/postgres/dsn.go:63`; pgx가 DSN options로 PGOPTIONS를 덮어씀. env만 설정한 Job | 긴 잠금 대기 위험. helper `source/readonly.go.txt:31`에서 exact pinned DSN의 유효 options 및 직접 timeout 키 부재를 검사. Job/SPC 동일 VersionId 고정. 네 synthetic override 거부, 실제 pinned metadata·mount 일치 확인 |
| 이전 세션 정리 미확인, 원인 미확정 | 기존 helper `.local/pr196-schema-tunnel-20261123/main.go:206`, SQL 성공 후 종료 ACK 확인 실패 | 이전 packet의 NOT_VERIFIED 보존. 새 helper는 exact-session History=Terminated·Active 부재·local port 폐쇄·process wait를 모두 확인. 이번 각 조회에서 실제 종료 검증. 과거 실패를 성공으로 치환하지 않음 |
| 수집 경로 미검증, 확정 제품 결함 아님 | 실제 baseline pods/proxy9090 timeout; NetworkPolicy9090은 Prometheus source만 허용 | localhost exec 건강점검과 공통 collector 경로 인수 구분. 정책/권한 확대 없음. 원인 후보이며 실제 source IP/집행 인과 미확인 |
| 기존 TLS 설정 한계 | pinned DSN parse: TLS enabled, hostname_verified=false | verifier의 verify-full을 migrator에 전용하지 않음. 공유 Secret 변경 없이 기존 encrypted 연결 한계를 명시. 신규 exploit/회귀 또는 host 인증 PASS로 주장하지 않음 |

## 요구사항 추적

| 요구사항/AC | 구현·consumer | 자동 검증 | 실제 Evidence·결과 | 한계 |
| --- | --- | --- | --- | --- |
| exact target/source·승인 범위·merge 금지 | coordinator identity/deployment/CAS, 고정 be4 source·corrected digest, 사전 공지 | readonly target/env3test, coordinator negative mocks | preflight·STSRDSUID·imageID·PR metadata, 원격 main/HEAD 불변 | scoped 사용자 실행 지시이며 DR GO 아님 |
| DB backup·부분 commit·호환 복귀 | existing Migrate/planPending, Job backoff0/deadline, fixed mount version | TestPlanPending6subtest+parent7PASS events | snapshotavailable→Job4파일→DB1–15checksum15/15·제약valid | snapshot restore 시험 없음; DDL conflict/commit-loss 실주입 없음 |
| 원래 모드와 여러 Pod 기본 기동 | Runtime default MAINTENANCE/local 유지·image-only N2 | 기존 exact-source CI는 이전 감사 참조, 이번 재실행 아님 | runtime/local-health before/candidate/restored, imageID·restart0·livez/readyz 성공 | durable Pause/Resume/claim/dispatch 기능·실 Provider 미시험 |
| 실패/불명/동시 변경 진행 차단 | helper readonly/generic error, coordinator CAS/INTENT·one-shot | stale/future/unknown session/timeout/ledger/secret/constraint/busy/concurrent spec 거부6test | 실제 receipt/현재 상태 대조; 새UNKNOWN 발생 없음 | 외부 변경 경쟁·API 응답 유실을 실제 주입하지 않음 |
| 안전한 기존 image 복귀 | fresh 업무0·모든5controltables0 조건, old tag digest 재확인, fullspec CAS | busy/controlrows/concurrent spec mocks | pre-rollback/final DB + old imageID·2Ready·원래 Argo spec·SyncedHealthy | durable 이력 사용 시 이 rollback 경로를 허용하지 않음 |
| 시험 자원만 정리 | Job/SPC label·생성 receipt UID 확인, UID/RV DeleteOptions | source review, 실제 UID 사전조건 API 요청 | cleanup receipts·remaining Job/Pod/SPC0·기존JobUID 보존 | snapshot/ECR 후보·schema15는 명시적 잔여 |
| DR 근거 과대 해석 금지 | D-24/Jira/runtime safety 경계 유지 | 과거 exact-source 안전 계약 회귀는 참조만 | 모든 receipt/summary HOLD·false 유지 | ReplicaLag FAIL·4NOT_VERIFIED·전체 DR 미인수 |

## 테스트·수집 방법

- 이번 실제 로컬 실행: readonly helper3개 PASS(DSN override4조건 포함), coordinator unittest6개 PASS(Mock),
  TestPlanPending6subtest+parent7PASS 이벤트. 초기 helper2개 PASS 이력과 마지막3개를 합산하지 않는다.
- 위 테스트에 skip 없음. 전체 Go/Web/CI·폐기 PG 회귀는 제품 source 미변경으로 반복하지 않았고 이전 exact-head 증거와 분리했다.
- 서버 dry-run은 Job/SPC API schema 수락이며 CSI/DB 실행 성공이 아니다. 실제 Job exit0/실제 mounted VersionId·DB checksum을 별도로 확인했다.
- 실제 RDS·SSM·EKS·ECR 조회, encrypted snapshot 생성, 4개 migration DDL, N2 rolling candidate/기존 image 복귀,
  Argo 일시 동결/복원·시험 자원 정리를 수행했다. 신규 IAM/RBAC/Secret값/기존 SPC/Worker opt-in 변경 없음.
- 직접 Pod proxy timeout은 실패 원본으로 유지했다. 후속 같은 proxy 호출은 반복하지 않고 NOT_RUN으로 구분한다.
  exec busybox wget localhost9090의 성공은 다른 method로 기록했다. HTTP 사용자 여정·브라우저 로그인·WSS·실 Provider E2E 아님.
- API JSON은 파싱/UTF-8 재직렬화된 수집 자료다. `migration-job.logs.jsonl`은 native stdout→PowerShell Out-File UTF-8 텍스트 수집이며
  원래 stdout 바이트와 동일하다고 주장하지 않는다. 줄바꿈/인코딩 처리 후 수집본 SHA를 기록한다. 한국어 U+FFFD 검사0건 확인.
- DSN/password/세션토큰/개인키/raw Provider/terminal transcript를 포함하지 않았다. secret-scan 결과와 packet SHA manifest를 별도 기록한다.

## 보안·실패·수명주기

읽기 전용 helper는 Secret을 RAM에서만 처리하며 명령 인자/로그에 넣지 않는다. 고정 CA·hostname/TLS, Loopback SSM route,
read-only transaction·ROLLBACK과 bounded query/cleanup을 강제한다. 실제 migration은 기존 Secret DSN의 TLS 한계를 보존한다.
Cloud API/SQL 실패 원문은 coordinator가 공개 출력하지 않는다. one-off receipt는 범용 durable distributed 승인 저장소가 아니며
실제 제품 INTENT/UNKNOWN의 안전성 증거로 대체하지 않는다. 모의 결과를 기반으로 실행 승인을 만들지 않는다.

기존 사용자 없음은 사용자 진술, inventory0은 시점 관측이다. maintenance를 open으로 유지한 이번 trial은 지속적인 접수 차단을 입증하지 않는다.
additiveDDL은 source DB뿐 아니라 정상 복제에 반영될 수 있으나 DB 승격·DNS·오사카 앱 활성화는 하지 않았다.
원래 이미지/spec과 Argo 정책 복구를 검증했으며 예상하지 못한 work/journal이 나타나면 자동 legacy rollback하지 않도록 했다.
이 회차에서는 no-op 업무 관측 하에서만 복귀했다. 제어 state/journal을 지우거나 실제 UNKNOWN을 자동 재시도하지 않았다.

잔여: schema15, 신규 수동 snapshot(자동 만료 없음/11월30일 재검토), 이전 ECR 후보 저장소/이미지.
오사카·서울 전체 teardown이나 기존 공유 자원 삭제를 요청받은 작업이 아니므로 실시하지 않았다.

## 독립 리뷰와 판정

Reviewer `audit_pr196_operator`가 실행 계획·helper·controller 및 실제 packet을 단계별 독립 검토했다.
Reviewer는 실제 AWS/DB를 다시 호출하지 않았으며 제출 원본과 source/test를 검토한다. 최종 보고서를 `independent-review.md`에 보존한다.
이번 대상인 DB schema 적용·legacy N2 image 호환성과 baseline 복귀는 `PASS_SCOPED`로 한정한다.
**PR196 전체 병합 가능, Stage3 완료, durable 운영 기능 또는 전체 DR GO 판정은 하지 않는다.**
공통 collector 경로·운영 승인/Policy·IAM/RBAC 인수·다중 Pod durable 경계·실 Provider/UNKNOWN·Fencing/Single Writer는 후속이다.
이 감사는 push/merge/외부 전송 권한을 부여하지 않는다. 최종 독립 검토와 manifest 검증 결과를 함께 인계한다.

## 전체 로컬 변경 보안 검사 추가 검토

신규 migration packet 단독 Gitleaks 8.28 검사는 exit0/탐지0이다. 이후 수정·미추적 비ignored 파일167개로 확대한
검사는 exit1/6건이므로 전체 scanner PASS로 표시하지 않는다. 최초 실패 로그와 redacted 결과를
`tests/whole-local-secret-scan*`에 그대로 구분해 보존했다.

6건 모두 이전 candidate-publication 원본의 generic-api-key 오탐으로 분류했다. 네 건은 migration10 공개 체크섬이며
Git LF 원본 및 실제 DB 체크섬과 일치한다. 한 건은 공개 Go 모듈 무결성 h1 값, 한 건은 독립 리뷰의 권한 설명 문장이다.
해당 파일·라인·분류·검토한 줄의 SHA를 `tests/whole-local-secret-scan-review.json`에 기록했다.
확인된 인증정보는0건이다. 기존 Evidence 수정이나 scanner 규칙 완화 없이 탐지와 수동 판정을 함께 유지한다.
신규 packet 단독 통과를 전체 변경의 무조건적 자동 보안 검사 통과로 확대하지 않는다.
