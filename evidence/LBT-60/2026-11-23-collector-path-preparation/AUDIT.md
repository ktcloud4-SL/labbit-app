# Pre-merge audit — Collector 경로와 승인 시험 준비

2026-11-23 · 사용자 `PRE_MERGE_AUDIT.md` 전문 및 repo AGENTS/CONTRIBUTING/TESTING 적용.
repo `ktcloud4-SL/labbit-app`, branch `codex/feat/LBT-60-stage3-live-prep-20261123`.
base/main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`.
최종 origin/main fetch 결과 동일. 로컬 미커밋 delta는 source manifest와 전체 status/diff로 고정한다.
이 감사는 push/PR/merge/배포 승인이 아니다. 현재 PR196 Draft·미병합/전체 DR HOLD 유지.

## 범위·검토 근거

- 최신 사용자 지시: 공통 수집 경로 확인/보완 후 실제 승인 기반 Pause/Resume 시험 준비,
  권한/NetworkPolicy/서울 영향 변경은 실행 전에 알림, merge 금지. 서울 사용자가 없음을 추가 확인했다.
  두 임시 network 작업창을 각각 사전 공지했다. 그 외 새로운 제어 DB 조작/승인/업로드 권한으로 확대하지 않았다.
- 부모가 이번 turn에서 연결된 PR196·Jira LBT-60 최신 코멘트·Confluence D-24 v13을 읽었다.
  PR HEAD/main 불변 및 #11265–11267의 scoped 코드 QA/실환경 미검증 경계를 확인했다.
  [D-24](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6815749),
  [LBT-60](https://samsunglions.atlassian.net/browse/LBT-60),
  [P2 QA #11267](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11267),
  [PR196](https://github.com/ktcloud4-SL/labbit-app/pull/196).
- Owning Runtime0.1.14 `runtime/dr-control.md`, Policy/Strict/CheckReference, AWSCLI reader,
  두 operator CLI consumer, collector/KubeCLI/SQL reader, Worker resume/restriction proof·timeouts를 대조했다.
  기존 M194/M196·Worker control·migration12–15 및 앞선 실제 schema15 packet을 참조했다.
- HTTP/OpenAPI·Connector/Realtime wire·DB migration·server admission/Worker 전이·SQL은 이번 제품 delta에서
  변경하지 않았다. 새 필드는 public HTTP가 아닌 보호된 operator Policy다. Runtime owning 문서를 함께 고쳤다.
  YAML/JSON schema는 그대로여서 Contracts 파서 재실행은 해당 없음; Go Strict/Policy consumer tests는 실행했다.
- main…HEAD 기존 PR 전체 파일 inventory를 기록했다. 변경하지 않은 기존 PR 코드는 정확 be4d0ba의
  기존 공식·독립 audit/CI를 재사용했다. 이번에 과거 PR 전체를 새로 구현·실환경 검증했다고 주장하지 않는다.
  모든 current delta(아래)와 consumer·ignored 실행 helper는 이번 검토 대상이다.

## 수정 전 findings와 처리

| ID·심각도 | 위치/재현·영향 | 보고/수정·회귀·실측 |
| --- | --- | --- |
| F-SG / P2 | `.local/pr196_collector_network_window2.py` cleanup: revoke 결과 Return=false/누락이어도 성공 출력 가능. 정상 정리처럼 오인할 위험 | 독립 reviewer 지적·사용자 공지 후 실제2차 create 전에 수정. receipt 보존 뒤 strict `Return is True` 아니면 HOLD. false/누락 회귀 포함5 Mock PASS; 실제 Return=true 및 전체 원래 SG 복귀 readback 별도 |
| F-REGION / P2 | HEAD `internal/drcontrol/awscli.go:98`는 S3 Head/Get에 제어 Policy.Region을 사용. 서울 제어+오사카 원본 조합에서 읽기 실패. 새로운 불변 원본 대신 기존 Synthetic로 실제 재현 | 사용자 사전 보고 후 optional ArtifactRegion·두 값 allowlist·빈값 fallback 추가. 현재 `contract.go:58,210`, `awscli.go:97`; 테스트 `awscli_test.go:59,134,148`. STS/RDS 독립·잘못된 값·version/hash변조·취소·temp정리 검증. 수정 후 서울 Target/오사카 원본 실제299B 일치. 승인 consumer 시간/서명 검증은 별도 |
| F-PACKET / 로컬 감사 도구 | 초기 assembler의 파일 존재 기반 manifest 경로 선택이 root README와 packet README를 혼동해 무결성 검사 중단. cloud/source mutation 없음 | 발견 공지 후 manifest schema→명시 root/packet 기준으로 수정, unknown schema/path escape 거부. 기존3packet 원본 전수 해시 비교 성공. 실패를 AWS/제품 결함으로 기록하지 않음 |

초기 ENI group-only 목록은 inventory였고 mutation 권한 근거가 아니었다. reviewer가 지적한 계획 caveat를 반영해
exact EKS service-owned ENI/requester/description/VPC/subnet/source를 새로 검증한 뒤 두 `/32`만 생성했다.
이는 실제 proxy SNAT source를 packet capture로 입증한 결과는 아니다.

## 전체 로컬 delta 검토

| 파일/묶음 | 목적·영향 경계·검토 |
| --- | --- |
| `internal/drcontrol/contract.go` | protected Policy에 optional ArtifactRegion, exact2값 allowlist. Strict duplicate/unknown 필드·Target.Region/control 일치·키/참조 제한 유지. 빈값 omitempty로 기존 JSON 직렬화 호환 |
| `internal/drcontrol/awscli.go` | immutable S3 Head/Get의 endpoint/--region만 선택. STS Policy.Region, RDS Database.Region 유지. 자동 검색/retry/fallback 없음 |
| `internal/drcontrol/awscli_test.go` | 기본2지역·교차2방향, endpoint/owner/version·readback·cleanup, unsupported endpoint 입력·hash/version/취소 거부 |
| `runtime/dr-control.md` | owning optional필드/기본값/허용범위·보관리전과 제어/DB리전 구분. 외부 HTTP/schema 변화 없음 |
| `docs/backend/dr-maintenance-integration.md`, `dr-stage3-deployment.md`, `dr-pr196-trial-preparation.md` | 기존 준비/후속 실제 실행 시점을 구분하고 새 진단·준비 문서 연결. 기존 표를 현재 PASS로 바꾸지 않음 |
| `docs/backend/dr-pr196-approved-control-trial.md` | T01–T10, actual 미확정 값·실제 DB 경로·2초 지연·승인 storage/외부 proof·durable rollback 경계 명시 |
| 새 collector packet | sanitized AWS/Kube metadata·임시 mutation intent/receipt/원복, 실제 S3 reader결과, Mock/Go/Web logs·source·audit·manifest. source bytes별 tested state 연결 |
| 이전3개 untracked packet | prior manifest 자체 보존. 원본 API/실행 Evidence 해시 그대로. 새 문서 연결만 intentional supersession으로 기록 |
| ignored helpers/binaries | source/ 사본과 binary hash/도구 입력 의존성 기록. 기존 rollout helper는 모듈 의존만; 이 turn에서 migration/rollout action 호출 없음 |

## 요구사항 → 구현 → 테스트 → 실제 근거

| 요구사항/AC | source/consumer | 자동 검증 | 실제 근거·한계 |
| --- | --- | --- | --- |
| GET-only TLS고정 공통 수집 | collector_adapters KubeCLI Snapshot/Process, route probe 고정 server/context/CA | Kubernetes adapter8 PASS Mock | actual Snapshot2회, 조건부 proxy livez2성공. legacy status404이므로 Process/fullCollector는 NOT_VERIFIED |
| 최소 영향/target ownership | NP 별도 create, fresh ENI 정확2개, node SG9090/32, receipt UID/RV/RuleID cleanup | NP5+SG5 Mock, -O각5; wrongownership/duplicate/source·falseReturn 거부 | Window1NP/Window2NP+SG 실제생성·삭제; 원래 NP/SG/Deployment/Argo/N2원복. 전파/conntrack 원인 분리는 미실측 |
| 제어 리전과 Evidence보관 분리 | Policy.ArtifactRegion + reader Head/Get, Runtime field | defaults·양방향·STS/RDS 독립, invalid regions fail-before-runner | 기존 오사카 immutable 원본299B: 수정전 서울실패·오사카성공, 수정후서울제어/오사카보관일치. 새 prefix 쓰기/승인 소비 미시험 |
| owner/version/size/hash/temp 보안 | 기존 Read CheckReference→Head→conditional range Get→SHA, context/remove | default/crossregion corruption/version/cancel/temp cleanup | 실제 fixed version/size/SHA 일치. 실제 변조/응답유실/취소를 AWS에 주입하지 않음 |
| fail-closed·예전 FAIL보존 | 기존 resume/restrict/collector proof 의미 유지, Plan disabled | 영향4pkg Race297PASS | 실제 DB 미연결/외부 proof미확인→실행 안 함. Reader2.43초는 callback2초PASS아님; HOLD보존 |
| 안전한 lifecycle/복귀 | 임시자원 exactcleanup, durable이력 후 legacy금지, T05/T10 | ownership/cleanup/UNKNOWN 기존 테스트 | 네트워크만 원복. durable 사용/UNKNOWN 발생/실제 paused복귀/Provider 정산은 미실행 |
| no merge/서비스과대인수 | 기존 server/source/image 유지, 새 operator만 빌드, falseflags | diff/manifest/scan·독립review | 원격push/merge/Slack/승인업로드 없음. DR4개미검증/ReplicaLagFAIL/HOLD 유지 |

## 실패·보안·수명주기 점검

- 입력: ArtifactRegion은 임의 URL/옵션/대소문자/공백/타리전 거부. 별도 보호 Policy이므로 승인 서명 범위를
  임의 endpoint 입력으로 넓히지 않는다. 기존 Strict·max256KiB·account/target·reference owner 제한 유지.
- 원본: 한 reader 인스턴스에서 Head/Get이 같은 고정 리전을 사용한다. ETag+VersionID+Size+SHA가 다른
  응답을 거부하고 context 만료 시에도 성공 반환하지 않는다. 삭제 marker/latest/null 버전 금지 그대로다.
- 재시도: 제품 endpoint 자동 탐색/복제/리전간 failover 없음. mutation helper는 exclusive intent 후1회 호출,
  응답 불명 시 보존/조사하며 성공 추정·create/delete blind retry 금지. 실제 응답 유실은 주입하지 않았다.
- 동시성/소유권: NP는 created UID/current RV로 삭제. SG는 exact saved RuleID를 제거하며 AWS API에 CAS는 없다.
  직전 재조회와 좁은 작업창으로 위험을 줄였지만 외부 동시 삭제/교체 경쟁을 실증한 것은 아니다.
- 재시작: 이번 제품 delta는 durable state/CAS/epoch/journal을 수정하지 않는다. 임시 helper intent/receipt를
  남기고 원본이 없으면 자동 재개하지 않는다. durable init 이후 과거0행/old-image 복귀 조건은 재사용 불가.
- 연결: 임시 TCP9090 허용은 path별 HTTP ACL이 아니라 그 포트 전체다. source32·pods/proxy RBAC·실행자
  통제 필요. 공용 ALB/Istio/Service/0.0.0.0/0 신규허용 없음. 기존 SG egress 설정0.0.0.0/0은 원본과 동일하다.
- 수명주기: 새 NP2/SG2규칙은 정리됐다. 기존 snapshot/ECR/schema15는 앞선 승인된 잔여로 남고 비용/보존
  재검토는 앞선 packet을 따른다. Secret값/키·DSN 공유·기존 NP/SG 관리규칙 변경은 하지 않았다.
- 보안: source/에는 공개 key와비민감리소스ID만, 인증 cache/PKCS8/DSN/Provider원문/Terminal내용 없음.
  일반 cloud stderr 저장 안 함. 최종 scan의 발견 여부·분류/제한은 scan summary를 따른다.
- 외부 정합성: health成功/404/APImetadata/SQL0/서명은 Provider완료·SingleWriter/전체DR GO 대체근거가 아니다.
  네트워크 원인·지연 상한·운영 최소권한·보호 key실배포·원본 Writer·공통DB접속은 별도 미검증이다.

## 테스트·Evidence 구분

보완 후 Race297 PASS/0FAIL/0SKIP, Go전체4671 PASS/88SKIP/0FAIL, vet/build 성공.
Go pass 수는 parent/subtest 이벤트·캐시재사용을 포함한다. package 캐시와 skip 이름은 test-summary에 전부 있다.
Web35파일493 PASS 및 typecheck/lint/build. npm PATH 부재로 동일 Node entrypoint를 직접 실행했다.
jsdom canvas 경고는 보존했으며 실패로 숨기거나 browser E2E로 확대하지 않았다.
Windows raw gofmt-l은 CRLF로 exit1. 원본 목록 보존 후 go/format.Source와 canonicalLF로 tracked전파일 대조,
실제 format차이0. Linux 파일시스템/신호/Provider fixture와 PG integration은 이번 재실행이 아니며 이전 감사 참조다.
raw Kube/AWS JSON은 parsing 후 재직렬화한 수집본이다. stdout PowerShell 로그도 transport 인코딩을 포함할 수 있다.
수집본 SHA는 wire원본byte 동일성 주장이 아니다. product에서읽은 S3내용hash는 exact다운로드bytes 검증이다.
초기NP-only실패·S3region실패·P2보완전테스트·포맷검사실패를 삭제하지 않았다.

## 독립 검토와 판정

사용자 지침에 따라 구현과 분리된 `audit_pr196_operator`가 원본 계약/consumer·helper·Mock·실제 receipt를
검토했다. reviewer는 AWS/공유DB/서비스/개인키를 직접 호출하지 않았다. 실제 route/S3 read는 부모가 수행했다.
F-SG 수정 확인, ArtifactRegion product4파일 독립 검토/3pkg unit PASS, 새로운확정 blocker없음.
과거 PR범위의 exact-head audit는 재사용이며 이번 전체 historical PR재검토와 구분한다.
최종 packet/계획의 추가 검토는 `independent-review.md` 후속 절을 따른다.

**이번 경로 진단·operator 보관리전 보완은 PASS_SCOPED. 실제 승인 제어 시험 준비는 보완 필요.**
실제 DB verified 경로, 새 signed원본 저장권한/고정참조, callback2초경로 검증, 외부 Safety/Provider 근거와
실제 다중Pod후속시험이 남았다. 임시 네트워크는 원복해 두었다. live_pause_resume_ready=false를 유지한다.
PR전체병합가능/Stage3완료/DR GO를 선언하지 않는다. 다음서비스영향변경 전 사용자에게 범위·복귀안을 먼저 알린다.
