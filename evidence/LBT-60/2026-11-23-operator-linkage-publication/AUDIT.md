# PR196 운영 제어 연결 증분 공개 감사 — 2026-11-23

## 범위와 승인

사용자가 기존 Draft PR196 반영(push)을 명시 승인했다. AWS/서울 서비스 조작·merge·외부 메시지 전송은 승인하지 않았다.
별도 local branch `codex/feat/LBT-60-stage3-live-prep-20261123`의 후보를 기존 PR head branch
`codex/feat/LBT-60-dr-admission-block-20261119`에 fast-forward만 수행한다. main/tag/force push는 하지 않는다.

PRE_MERGE_AUDIT.md와 저장소 AGENTS/CONTRIBUTING/TESTING을 다시 읽었다. 최신 fetch 기준 main
`753eecbff737409a4c9cc8c542f3c6baa6acf0da`, PR196/로컬 시작 HEAD `94fe1f61d7e398761291a967fab9820bfb08bc3d`가
직전 구현 감사와 동일하다. Jira LBT205(11/23 09:04), LBT60(09:59), Confluence9666637 v4 원문도 다시 확인했으며
요구 변경은 없다. 이 읽기 외에 Jira/Confluence 댓글이나 메시지를 쓰지 않았다.

## 전체 diff·SSOT·실패 검토

전체 base→HEAD 및 로컬/untracked는 [구현 감사](../2026-11-23-operator-linkage-offline/AUDIT.md),
파일별 inventory·원본 SHA·요구→구현→테스트→실환경 공백 표에 연결했다. 공개 파일은 publication-source.json에
테스트 당시 raw SHA, LF 정규화 SHA, 예상 Git blob을 함께 고정한다. Evidence는 기존 evidence/.gitattributes의
`* -text`를 따라 원본 바이트를 보존하며 index blob을 별도로 비교한다.

기존 두 배포 문서와 이전 readiness27개를 포함한다. readiness는 **이전 실제 관측**이며 새 연결 코드의 AWS
검증으로 합산하지 않는다. 이번 구현 검증은 Mock/Fake, 새 loopback 폐기 PG16, noNIC RAM Linux에 한정된다.
기존 94fe CI는 새 미커밋 구현을 검증한 결과로 표시하지 않는다. 새 공개 HEAD CI는 push 이후 별도 확인한다.

승인·target/Plan/요청 hash/목적·키·TTL·역할 불일치 거부, INTENT/APPLIED 원자성·UNKNOWN 보존·자동 재Apply 금지,
scope 경쟁·stale incarnation, 제한 pending의 drain/Resume 차단, SQL read-only와 Pod 전체 roster·전후 snapshot,
missing vs unsafe 판정·외부 FAIL 보존·protected key 파일/Policy, Admin-only readback, 신규 additive migration15와
기존 migration1~14 불변을 검토했다. LINK01~03의 수정 전 findings와 해결·원본 실패·skip 기록은 구현 감사에 남아 있다.
새 제품 변경은 없으며 공개 준비를 위한 문서/manifest만 추가한다.

## 재사용한 동일 소스 검증

- 전체 Go4604 PASS/88SKIP, 영향6package race335 PASS, 폐기 PG 전체 race1761 PASS/10SKIP,
  분류 수정 후 PG/handler21 PASS, Linux drcontrol124 PASS, Web493 PASS 및 typecheck/lint/build,
  format/vet/build·Runtime/migration metadata 검증을 재사용한다. 환경별 결과를 합쳐 고유 test 수로 취급하지 않는다.
- 전체 회귀 종료 후 마지막 변경은 기존 init/pause test에서 실제 protected-key SignApproval→소비자를 검증하는
  test-only 보강이며, 최종 영향 race와 Linux에 포함했다. 제품 소스는 전체 회귀 이후 불변이다.
- 독립 operator 리뷰의 64개 source/Evidence SHA와 실행 source277개, 구현 packet60개 artifact SHA를 재검산했다.
- 공개 예정 파일 export에 Gitleaks8.28 검사: 탐지0. Raw log scan 탐지0도 기존 구현 packet에 있다.
- 폐기 PG test DB 잔여0/해당 서버 종료, noNIC guest 정상 종료를 기존 원본으로 확인했다. 이번 공개 작업에서는
  PG/VM/AWS/클러스터/Slack을 재실행하지 않는다.

## push 영향

현재 .github/workflows 전부를 다시 대조했다. 해당 파일과 Dockerfile/dependency에 이번 추가 변경은 없다.

- Backend는 pull_request에서 Docker build만 하고 `push:false`; AWS OIDC/ECR 조건은 `event != pull_request`,
  GitOps image 갱신은 `push && refs/heads/main`에만 실행한다.
- Web image job은 PR에서 제외되고 Connector release는 connector tag push/수동 실행만 받는다.
- Go/Web/Contracts/Security는 GitHub runner의 unit/mock/local fixture 검사다. PR branch push가 서울 배포를 하지 않는다.
- merge 이후에는 기존 배포/migration이 실행될 수 있으므로 Draft·미병합을 유지한다. 운영 키/Policy·migration15의
  백업/선적용·복귀, 고정 image와 GitOps, 전체 Pod/Job 정산, 실제 Fencing/SingleWriter·Provider는 별도 Gate다.

ReplicaLag FAIL·필수 Gate4 NOT_VERIFIED·execution_gate HOLD·recovery_authorized=false·cloud_verified=false를
유지한다. 코드 후보 공개/CI 성공을 Stage3 완료·DR 승인으로 확대하지 않는다.

독립 공개 검토 보고서를 같은 디렉터리에 첨부한 후 index 무결성 검사와 함께 커밋한다. 해당 검토 및 index 결과에서
새 차단 결함이 없을 때만 기존 PR head에 fast-forward한다. CI/원격 HEAD/Draft 확인은 최종 답변에 구분해 보고한다.

독립 공개 검토 결과: source277·artifact60·이전 readiness26·기존 snapshot64의 hash가 일치하며 새 P0/P1/P2
차단 finding은 없다. 압축 원본을 포함한 민감정보 pattern 검사도 탐지0이다. 보고서는 `independent-review.md`다.
Git index125개도 tested raw/LF source와 일치했다. 원본 보존된 CRLF Evidence가 기본 diff whitespace 검사에서
trailing CR로 표시돼 `cr-at-eol`을 인식하는 검사로 재확인했으며 실제 trailing whitespace 결함은 없었다.
이 확인을 위해 원본 Evidence 줄바꿈이나 제품 formatter·검증 정책을 변경하지 않았다.
