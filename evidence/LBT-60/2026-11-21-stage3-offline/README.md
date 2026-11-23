# Stage 3 AWS 중단 중 작업 인계

2026-11-21 KST. AWS/서울 서비스/공유 DB/Argo/Slack에는 접속하지 않았다.
세 저장소의 새 로컬 브랜치에만 변경했으며 commit/push/merge/deploy하지 않았다.

| 순서 | 이번 변경 | 실제 운영에서 남은 조건 |
| --- | --- | --- |
| 1 | 별도 `labbit-dr-control` 운영 CLI 후보: preview 기본, exact STS/Principal/Scope, 서명 승인·관측, 고정 S3 VersionId·SHA·TTL, Resume/Inspect | 신뢰 수집자·키 운영·실제 IAM/TLS/대상·승인 작업창 인수. 가짜 승인으로 실제 상태를 대신하지 않음 |
| 2 | PR196 migration12–14·고정 image·백업/복구·작업창·정리 계획 | 실제 스키마/이미지/백업/복원/최소권한 검증 |
| 3 | 신규 접수 closed와 승인 Worker 분리 후보 `APPROVED_CONTROL`; 기본 #194 정책 유지 | 전체 backlog 정산·durable Pause·새 승인과 binding·실제 모든 Pod coverage 필수 |
| 4 | GitOps prepared/standby/active-paused/reconcile/active/serving 후보 values와 Helm 렌더 | Root Application 미등록. 실제 image/digest·Secret·DB·접속 격리·전환/복귀 검증 |
| 5 | Infra 22단계별 Adapter/필수 Evidence/입력 계약, 전체 Pod·작업 목록 정합성 모의 검사 | 실 Fencing/Single Writer/정산 Collector, 전역 실행 소유권, RDS/GitOps/DNS 실행 Adapter는 미구현 |
| 6 | 실패·위변조·중복·만료·stale binding·UNKNOWN·역사적 재조회 회귀 테스트 및 독립 감사 | AWS 재가동 뒤 #194 남은 시험+#196 실제 여러 Pod 시험, 이후 전체 DR 시험 |

## 작업 위치

- App: `codex/feat/LBT-60-stage3-offline-20261121`, base `c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac`.
- GitOps: `codex/feat/LBT-60-stage3-profiles-20261121`, base `ab830d7c9e046b0621477192aca39c6b269cbf30`.
- Infra: `codex/feat/LBT-60-stage3-adapters-20261121`, base `5987cb061092d406ba8effaf3259d56e260246b1`.

세 저장소 모두 이 폴더의 상위 `tmp` 아래 별도 worktree다. 원래 App 작업 폴더의 미커밋 8개 파일은
이 후보에 복사하거나 수정하지 않았다. 원격 PR196/57 및 배포 상태는 이번 로컬 작업과 다르다.

## 중요한 실행 경계

`APPROVED_CONTROL`은 maintenance closed만으로 Worker를 멈추는 정책이 아니다. HTTP closed를 유지하며
authoritative 영속 제어가 Worker의 claim/prepare/dispatch를 결정한다. 별도 Pause, 전체 작업 정산과
변경 동결 없이 사용하지 않는다. Operation ID별 허용 목록이 아니라 정산된 기존 backlog 처리 경로다.
기본 MAINTENANCE 정책과 #194 동작은 유지한다. RECONCILE_ONLY는 기존 typed query 경계를 따른다.

CLI의 서명 관측 입력은 신뢰 수집자의 검증 결과를 받는 포트다. 실제 Fencing/Single Writer를 직접 수집하거나
증명하지 않는다. `APPLIED`는 제어 journal의 역사적 명령 결과이며 Pod 재배포·maintenance 개방·DR 승인이 아니다.
리전별 복제 DB를 각각 독립적인 제어 Writer로 사용하면 안 된다. control scope CAS는 전역 DR 실행 잠금이 아니다.

실제 RDS 승격/DNS 변경·Global lock·안전한 운영 Pause·실수집자까지 모두 구현됐다고 주장하지 않는다.
현재 후보들은 연결 계약을 확정·검증한 범위다. **3단계 전체 완료가 아니다.**

ReplicaLag FAIL, REQUIRED_SETTINGS/RUNTIME_PROVENANCE/SERVICE_FUNCTIONS/SINGLE_WRITER NOT_VERIFIED,
`execution_gate=HOLD`, `recovery_authorized=false`, `cloud_verified=false`를 유지한다.

## 검증 기록

실제 명령/결과 및 실패·skip·재실행은 [AUDIT](AUDIT.md), 실행 소스 SHA256은 `source-manifest.json`을 따른다.
원본 로그와 요약을 혼동하지 않는다. 새 source는 미커밋이므로 base HEAD의 기존 CI를 새 source의 CI로 간주하지 않는다.
현재 전체 저장소 검사가 모두 green인 상태는 아니며 push/PR 준비 완료로 선언하지 않는다.
