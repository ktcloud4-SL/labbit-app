# PR196 DB·원본·2초 제한 보완 — 공개 감사

2026-11-23. 사용자가 보완 코드와 Evidence를 **기존 Draft PR196**에 반영하도록 요청했다.
이번 공개 작업은 Git/GitHub·로컬 검사만 사용한다. 새로운 AWS·서울 서비스·DB 조회/변경·Slack 전송·merge는 하지 않는다.

## 기준 및 전체 변경

- base/main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, parent/기존 PR HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`.
- feature `codex/feat/LBT-60-stage3-live-prep-20261123`의 검증된 후속분을 기존 PR head `codex/feat/LBT-60-dr-admission-block-20261119`로 fast-forward한다. main·태그는 push하지 않는다.
- 최신 Git fetch/원격 ref/PR API에서 base와 parent 불변, Draft/open/unmerged 확인.
- LBT-60 최신 댓글11271과 D24 v13을 재조회했다. Stage2C 과거 RUN의 네 NOT_VERIFIED는 새 Stage3 자료로 소급 PASS 처리하지 않는 원칙을 유지한다. latest 전체 댓글 수100은 이번 응답의 반환 수이며 모든 과거 댓글의 pagination 완료를 뜻하지 않는다.
- 기존387파일 PR은 정확 parent의 과거 감사·QA와 이번 전체 Go/영향 PG 회귀를 연결한다. 이번에 과거387파일 전부의 독립 구현 검토를 다시 했다는 주장은 하지 않는다.
- 새 제품/문서/CI21개는 원본 `../2026-11-23-db-artifact-validation/source-manifest.json`으로 식별한다. 이번 공개 준비에서 추가된 `.gitleaks.toml`만 별도 보정이며 아래 finding과 회귀를 연결한다.
- 원본5개 packet은 당시 시점 상태를 보존한다. 미커밋/미푸시/준비만/미실행 문구를 공개 결과로 소급 바꾸지 않는다. raw bytes→Git blob은 `index-verification.json`에 연결한다. Evidence의 기존 `-text` 설정을 유지하며 제품소스는 canonical LF를 대조한다. 생성된 `__pycache__`·개인 `.local`·키·DSN은 staging하지 않는다.

## 요구사항·실행 근거

| 요구·SSOT | 구현·자동 검증 | 실제 Evidence와 남은 경계 |
| --- | --- | --- |
| DB target/TLS 유지, 보호 Policy 선택 운송 | DatabaseTunnelPort·DatabaseConfig·VerifySQL, exacthost/network/TLS 부정 회귀 | 제품 경로 RDS193ms/readonly/TLS1.3, 세션 종료. DB 설정이나 Single Writer 인수가 아님 |
| 고정 원본·UNKNOWN 보존·안전한 수명주기 | 별도 공개 publisher, fsync/창 재확인/조건부 PUT/Head/Get, Fake12 normal/-O | 만료 SIMULATION2건 VersionId/SHA/AES256. 최초 PUT source hash 미보존·RequestId 미보존·독립Reader 미실행을 명시 |
| callback2초와 현재 서명/TTL·기본 maintenance 보존 | auth/historical 뒤 invocation bytes8초, inner Inspect/CAS, 기존 callback/CLI 제한 유지 | 실제 S3준비3719ms, 만료승인/SIMULATION 음성 callback. 정상 LIVE 승인·실제 Pause/Resume 지연 아님 |
| Runtime·producer/consumer 정합 | Runtime0.1.15 세 owning 문서, Makefile/Python Fake CI 연결 | 현재 운영/이전 후보는 과거0.1.14 source. 새 v0.1.15 후보 재배포 없음 |
| 기존 안전 Gate와 시험 이력 보존 | 단계별 원본/감사/manifest, no rewrite | 아래5packet 범위 구분. 과거 Stage2C FAIL·NOT_VERIFIED 그대로 |
| GitHub 반영이 서울 서비스 변경을 유발하지 않음 | delivery workflow 조건 재검토 | PR Backend image push:false·AWS OIDC/ECR/GitOps 갱신 skip. GHCR 로그인 단계 자체는 기존대로 실행됨. feature push가 배포 승인이 되지 않음 |

원본 `db-artifact-validation/AUDIT.md`에 수정 전 DBART-01/02 P2, 문서 P3, 실패·보안·취소·경쟁·재시작·원본 수명주기 분석과 독립 review를 보존했다. 이번 공개는 동일 제품소스이며 테스트 전체를 불필요하게 반복하지 않았다.

## 수정 전 공개 finding

**PUB-DBART-01 / P2 — CI 기본 검사와 이전 packet 전용 스캔 설정 차이.**
parent `.github/workflows/security.yml:35`의 PR `gitleaks detect --log-opts="origin/main..HEAD"`는 루트 설정을 사용한다. 이전 `candidate-publication/gitleaks-reviewed.toml`의 오탐 예외는 CI에 적용되지 않는다. 독립 `Gitleaks8.28 dir <candidate-packet> --config <root-config>`에서 exit1/6건을 재현했다. 대상은 공개 module checksum1·동일 migration SHA2564·도구 부재 목록 문구1이다. 이는 credential 유출 finding이 아니라 새 Evidence commit의 Security CI 차단이다. 수정 전에 사용자에게 보고했다.

루트 generic-api-key rule에 **고정6경로 AND exact literal match**로만 예외를 추가했다. 정규식 범위를 넓히거나 원본 Evidence를 지우지 않았다. `gitleaks-regression.json`은 기존6건 재현·수정후0건·literal6/path6 변경 시 각각 탐지되는14조건의 실제 결과다. 다른 값이나 경로에 동일 예외가 적용되지 않음을 확인한다. 전체 staged snapshot과 압축 로그 원본을 별도로 검사한다.

재현 절차: 고정6원본을 임시 폴더로 복사하고 parent `.gitleaks.toml`과 최종 루트 설정으로 각각 `gitleaks dir --config ... --redact` 실행한다. 원본에서 각 탐지 literal의 한 문자를 바꾸는6조건, 동일 literal을 다른 파일명에 두는6조건을 최종 설정으로 검사한다. 결과는 각각 exit1/1finding이어야 한다. 실제 credential을 쓰거나 새 광역 예외를 추가하지 않는다.

**PUB-DBART-02 / P2 — 공개 helper 재실행 시 자기 해시 참조.**
로컬 `publish_db_artifact.py`의 stage entries 루프가 이전 실행에서 생긴 `index-verification.json`과 `artifact-manifest.json`을 포함한 뒤 다시 덮어쓰므로 자기 항목 해시가 stale해지고 출력 파일 수도2개 과다 표기됐다. commit/push 전에 원본/실제 index 수 대조로 발견해 사용자에게 수정 전 보고했다. 제품 source·봉인 원본은 영향이 없다. 자기2항목은 명시적으로 제외하고 외부 manifest로 식별하며, 재생성 뒤 모든 entries와 publication 파일의 Git bytes를 다시 확인한다. index 파일 수는 제외2개를 한 번만 더해 실제 staged 수와 대조한다. 서로 다른 source를 테스트했다는 잘못된 연결을 방지하는 공개 도구 보정이며 실제 AWS 작업은 없다.

## 테스트·증거·독립 검토

- 제품 exact source: Go4684 PASS/88 SKIP, race4pkg310 및 최종3pkg210 PASS, disposable PostgreSQL16.15 영향3pkg703 PASS, Web493 PASS, Python12 normal/-O, vet/build/typecheck/lint/build/Runtime parse PASS. suite를 중복 합산하지 않는다.
- 첫 PG fixture-role 오류·첫 Web EPERM·harness 초기 실패와 skip은 원본에 남겼다. 현재 공개 단계에서 AWS로 다시 테스트하지 않는다.
- 최초 준비/후보 게시/Schema15 적용·후보 N2 기동·복귀/Collector 경로/이번 DB·원본·2초 검증의 서로 다른 source와 실행일시를 분리한다. 과거 snapshot restore·TLS 미검증이나 callback미충족을 최신 제한 시험으로 소급 지우지 않는다.
- 이번 v0.1.15 source는 앞선 be4d0ba/v0.1.14 시험 이미지에 포함되지 않았다. 후보 게시·migration 준비 기록을 새 Worker 제어 배포 성공으로 오인하지 않는다.
- 원본 최종packet80항목과 source21개, 이전 packet manifest와 Artifact, 압축5로그의 SHA·크기를 재확인했다. 독립 공개 reviewer는 제품 변경을 하지 않고 설정·원본·stage scope를 별도 확인한다. 원본 독립 코드감사와 공개감사는 구분한다.
- 새 GitHub 정확 HEAD CI는 push 이후 확인한다. 그 전에는 과거 be4d0ba 5/5를 새 source의 CI로 보고하지 않는다.
- 전체 `git diff --check`의 초기 실패는 원본 command 출력의 trailing spaces/EOF와 보존된 diff snapshot의 context space/tab 때문이다. `whitespace-check.json`에 실패 범위·출력 해시와 제품/설정/문서만의 검사 통과를 구분한다. 봉인된 원본을 formatting 목적으로 바꾸지 않는다.

## 한계와 판정

다운로드된 바이트와 Policy는 invocation snapshot이며 이후 IAM 회수/원본 삭제를 즉시 재조회하지 않는다. ACL과 영속 receipt 운영·수동 보존은 별도 책임이며 ObjectLock/자동 삭제를 보장하지 않는다. S3 샘플2개는 만료/SIMULATION이며 실제 운영 승인으로 쓰지 않는다.

실제 운영 승인 init/Pause/Resume, APPROVED_CONTROL 다중Pod claim/dispatch/drain·기수락/UNKNOWN·Provider 정산, Writer Fencing/Single Writer·Failover/Failback·공식 RTO/RPO는 남아 있다. 공개 감사는 이 경로의 실행 승인이나 PR 전체 merge 승인과 별개다. 기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false를 유지한다.

사용자의 Draft 업데이트 범위에 한정한 공개 준비를 검토하며 최종 독립 보고서·index 원본 일치·보안 검사로 판정한다. 실제 AWS 재조회/변경·서울 서비스 조작·동료 메시지 전송·merge는 이번 작업에서0이다.
