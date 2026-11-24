# PR196 migration 및 기본 배포 호환성 시험

2026-11-23 KST · LBT-60/LBT-205 · app source `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1` · main `753eecb`.
사용자는 서울 이용자가 없음을 알리고 다음 단계 실행을 지시했으며, migration/배포 전에 변경·복귀 방법을 요구했다.
본 채팅에서 snapshot 비용·DDL·Pod 재시작·Argo Sync 지연·기본 모드 및 복귀 조건을 사전 공지했다.
이는 전체 DR GO, PR 병합 또는 외부 메시지 전송 승인이 아니다. merge/push/Slack/Provider mutation 없음.

## 실행 및 최종 상태

- 서울 RDS를 독립 TLS verify-full SSM 터널로 READ ONLY 조회: schema1–11과 Git LF 체크섬11/11 일치.
  미완료 Operation/Item, 미정산 Provider DB 기록, 활성 Terminal, 타 열린 transaction은 각 관측에서0.
  이 값은 실제 Provider 정합성·Single Writer·지속적인 사용 부재의 증명이 아니다.
- 암호화 수동 RDS snapshot `labbit-seoul-pr196-before-v15-20261123-01`을 생성하고 available을 확인했다.
  **수동 snapshot은 자동 만료되지 않는다.** 서빈 관리, 2026-11-30T05:03:55Z 보존 필요성·비용 재검토 후 별도 삭제 판단.
  실제 snapshot restore 시험은 하지 않았다. snapshot 존재를 restore 성공으로 기록하지 않는다.
- root-app-seoul/backend의 자동 Sync만 UID/RV/spec CAS로 잠시 제거했다. GitOps main `ab830d7` 불변 확인.
  원래 source·revision·정책을 보관하고 image-only N=2 rolling 시험 후 모두 복원했다.
- 후보 digest `f3aa162b6cd5b28028c1681fb1daadffc73351b5e2785f0c7fb8ebd5a25b9469`로 고유 migration Job을 실행했다.
  ECR `backend-dr196-trial`, app source be4d0ba + runtime zlib1.4-r0 override이며
  [이전 provenance](../2026-11-23-candidate-publication/runtime-zlib14/image-provenance-chain.json)를 재사용한다.
  Critical이 검출된 초기 후보 digest9dc273는 사용하지 않았다.
- Job은 backoff0/restartNever/deadline180초, 기존 backend SA, 별도 SPC에 고정한 동일 Secret VersionId를 사용했다.
  Secret 값·기존 SPC·IAM/RBAC는 변경하지 않았다. PGOPTIONS의 실제 parser precedence를 사전 확인했다.
  Job 실제 imageID·Secret mount version·exit0와 schema12–15 적용 로그를 연결한다.
- 14:10:50 KST에 migration12–15 네 파일이 각각 commit됐다. 15개 체크섬은 Git LF 원본15/15 일치하며
  Worker 제어 테이블5개 모두0행, migration14 제약 convalidated=true를 재조회했다. 기존 business data를 삭제하지 않았다.
- 후보 Backend2개 Ready/restart0, 각 Pod localhost `/livez`·`/readyz` 성공을 확인했다.
  기존 env·replica·role·MAINTENANCE/local을 유지했다. 새 durable 제어는 opt-in하지 않았다.
- 제어/업무0 관측을 재확인한 뒤 기존 `backend:sha-753eecb` image와 원래 Deployment spec으로 복귀했다.
  기존 digest `bab1869e…42419`, N=2 Ready, 각 Pod 건강점검, root/backend Synced/Healthy 및 원래 정책 복원을 확인했다.
- 시험 Job·Pod·SPC는 UID/RV DeleteOptions로 정리하고 부재를 확인했다. 기존 migration Job UID는 그대로다.
  DB schema15·새 snapshot·기존 ECR 후보는 남아 있다. 제어 journal 삭제/down migration/전체DB복원은 하지 않았다.

## 실제 검증 경계

**SCOPED:** DDL 적용, 체크섬·제약·mount version·실제 후보 digest, 기본 N=2 기동, 기존 app/schema15 호환 복귀.

**미실행:** APPROVED_CONTROL, 실제 init/Pause/Resume·서명 승인 소비, 다중 Pod durable claim/dispatch 차단,
backlog/drain/committed replay, Provider, 실제 Fencing/Single Writer, Failover/Failback/RTO/RPO, HTTP 사용자 여정.
이 시험은 기존 M194/M196 전체 PASS 또는 Stage3 완료가 아니다.

**수집 경로:** 기존 Pod의 API server pods/proxy→9090 호출은 timeout으로 NOT_VERIFIED였다.
대신 `kubectl exec`로 localhost9090을 읽었고 결과의 method를 구분했다. 원래 실패는 보존한다.
현재 NetworkPolicy는9090에 observability/Prometheus만 허용하는 형태여서 원인 후보다.
RBAC 허용만으로 네트워크 경로가 열리지 않는다. 실제 source IP·정책 집행을 확인하지 않아 확정 원인으로 단정하지 않는다.
정책을 수정하거나 공통 collector 경로를 인수하지 않았다. 이 경로를 보완하기 전 LIVE collector 전체 성공으로 표시하지 않는다.

**TLS 한계:** read-only 검증자는 고정 CA·hostname 검증/TLS1.3을 강제했다. 실제 migration runner는 기존 Secret DSN을 사용해
TLS가 켜져 있지만 hostname 검증이 없는 설정이었다. 기존 설정을 변경하지 않았으며 migration TLS hostname 인증 PASS를 주장하지 않는다.

`ReplicaLag=FAIL`, 필수4 `NOT_VERIFIED`, `execution_gate=HOLD`, `recovery_authorized=false`, `cloud_verified=false` 유지.

## 원본 및 재현

| 자료 | 역할 |
| --- | --- |
| `preflight.json`, `baseline-applications.json` | 실행 신원·target·source·원래 Argo spec·image scan |
| `db-before.json`, `db-timeout-verification.json`, `db-pre-migration.json` | READ ONLY baseline, pinned Secret/timeout parser 검증, 작업0 관측 |
| `snapshot.*.json`, `snapshot-available.json` | backup 요청·응답·available; 복원 성공 증거 아님 |
| `migration-job*.json`, `migration-secret-provider-class*.json`, `migration-pod.json`, `migration-secret-mount.json` | 계획/dry-run·실제 image·mount·Pod exit0 |
| `migration-job.logs.jsonl`, `db-after-migration.json`, `schema-checksum-comparison.json` | 실제4파일 commit 및15개 체크섬·제약 |
| `runtime-*.json`, `local-health-*.json` | before/candidate/restored의 owner chain·실제 imageID·Ready·관측 방법 |
| `*.intent.json`, `*.receipt.json` | 단계별 write-ahead 기록; UNKNOWN 자동 재실행 없음 |
| `db-pre-rollback.json`, `db-final.json`, `final-readback.json` | 빈 제어 이력·업무 관측, 원복·정리·잔여 snapshot |
| `source/`, `tests/`, `AUDIT.md`, `artifact-manifest.json` | 실행 coordinator/readonly helper 원문·테스트·추적성·SHA256 |

source는 실행 당시 참고본이다. 절대 경로·소비된 RUN/Job/snapshot 이름·baseline UID가 들어 있으므로 그대로 재실행하지 않는다.
새 작업창·새 식별자·현재 target·새 원본·사용자 승인과 변경 전 공지가 필요하다. private key/DSN/password/token 원문은 포함하지 않는다.
이 packet은 로컬 작성이며 이번 요청에서 PR196에 push하거나 외부로 전송하지 않았다.
