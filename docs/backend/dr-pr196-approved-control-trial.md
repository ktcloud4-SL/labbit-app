# PR196 승인 기반 Worker 부분 시험 — 수집 경로 확인과 실행 전 Gate

**DB/보관/시간 제한 후속:** [원본 보관과 연결](dr-control-artifact-storage.md)에서
기존 SSM 운송을 사용하는 제품 TLS 경로와 만료 SIMULATION 원본2개 보관·고정 버전 읽기,
8초 바이트 준비/2초 현재 시각 재검증을 확인했다. 아래 표는 보완 전 준비 순서도 포함한다.
T02는 읽기 전용 SQL 범위, T03은 비실행 샘플 범위에서만 확인됐다. 실제 운영 승인과 전체
수집·Pod 적용·T05 복귀 안전 조건이 남으므로 `live_pause_resume_ready=false`를 유지한다.

2026-11-23 · LBT-59/60/205 · PR196 Draft/미병합. 이 문서는 실행 승인서나 입력 JSON이 아니다.
Owning 계약은 [Runtime](../../runtime/dr-control.md), 기존 [M194/M196 시험표](dr-maintenance-integration.md)다.
[이번 실제 기록](../../evidence/LBT-60/2026-11-23-collector-path-preparation/README.md)과
[앞선 migration·기본 모드 호환 시험](../../evidence/LBT-60/2026-11-23-migration-candidate-rollout/README.md)을 구분한다.

## 이번에 확인한 경로

- 고정 API server/context/CA를 사용하는 제품 KubeCLI의 전체 Pod Snapshot은 성공했다.
  기본 상태의 `pods/proxy → 9090`은 실패했다. localhost exec 결과를 공통 수집 성공으로 대체하지 않았다.
- 현재 EKS 관리 control-plane ENI 2개의 private `/32`만 대상으로 새 NetworkPolicy와 node SG TCP9090 규칙을
  일시 추가한 작업창에서는 두 Backend `/livez` GET이 성공했다. 원래 NetworkPolicy·Argo·Pod 설정은 수정하지 않았다.
  기본 MAINTENANCE/local 구성의 `/dr-control/status`는 두 Pod 모두404였다. 이는 계약상 예상되는 결과이며
  durable 상태/BootID 조회 성공이나 실제 전체 Collector 성공이 아니다.
- 임시 NetworkPolicy 삭제 후 SG만 남은 구간에서도 GET은 성공했다. conntrack/기존 연결/정책 집행 영향을
  분리하지 않았으므로 NetworkPolicy의 필요 여부나 단독 원인을 확정하지 않는다.
- 시험 소유 NetworkPolicy 2개와 SG 규칙 2개는 모두 제거했다. 전체 SG ingress/egress·원래 NetworkPolicy·
  Deployment·Argo spec 복귀와 Backend N=2 Ready를 확인했다. 다음 시험 때 경로는 새로 준비·재검증해야 한다.
- 기존 오사카 S3 고정 버전 원본은 서울 `Policy.Region` endpoint에서 실패했다. 제품 코드에 선택적
  `ArtifactRegion`을 추가하여 **서울 Target/제어 + 오사카 원본** 읽기의 VersionId·크기·SHA256 일치를 확인했다.
  새 승인 원본 생성/업로드·그 prefix의 Writer 인수는 하지 않았다.
- 수정 후 Head+Get 실측 합계는 약2.43초였다. 독립 Reader probe의 상위 context8초 결과이며,
  Resumer의 기존 callback2초 제한 통과 근거가 아니다. 권한/인증·배치/접속 지연까지 포함한 실제 경로가
  제한을 넘으면 HOLD다. TTL/timeout을 늘리거나 원본 검증을 생략해 우회하지 않는다.
- Windows→RDS TCP5432는 제한 시간 내 확인되지 않았다. 앞선 독립 SSM read-only helper의 SQL 성공은
  제품 CLI의 접속/TLS 성공이 아니다. 정확한 host/port와 verify-full 계약을 만족하는 실행 경로를 먼저 마련한다.

## 실제 시험 입력을 확정하는 순서

| 순서 | 준비·확인 | 실행/중단 경계 |
| --- | --- | --- |
| T01 작업창/대상 | admin3 exact ARN, 현재 이용자/작업/UNKNOWN, EKS UID·Deployment UID·schema15·직전 backup·후보 digest 재확인 | 관측은 실행 직전 새로 취득. 과거0건을 현재0건으로 재사용하지 않음 |
| T02 제어 실행 위치 | 보호된 Policy/목적별 key/DSN, exact RDS TLS verify-full, STS/RDS/S3 읽기·SQL 연결 | 현재 Windows 직접 DB 경로 미확인. VPC 내 격리 실행 또는 별도 검토된 SSM 연결을 비교; 권한/네트워크/hosts 변경은 사전 공유 |
| T03 원본 경로 | 서울 Region + 명시적 오사카 ArtifactRegion, 승인/관측 전용 prefix·Writer/Reader·버전/해시/보존, purpose/Phase·TTL | 과거 Synthetic의 읽기 성공은 새 prefix 쓰기 승인 아님. CLI는 업로드하지 않음. 승인 원본 byte 그대로 고정 버전 보관 |
| T04 수집 경로 | fresh EKS ENI ownership/IP·전체 Pod 계획·CA·RBAC, exact9090 임시 또는 IaC 권한안 | 이전 `/32`를 영구 허용값으로 복사하지 않음. 새 SG/NetworkPolicy 적용 전 영향·정리 계획 사전 공유 |
| T05 복귀 준비 | Argo 원래 정책·고정 GitOps/image, compatible paused 후보, UNKNOWN 처리·journal 보존 | durable init 이후 단순 old/local image rollback을 복귀안으로 쓰지 않음. 안전한 종료/유지 방안 없으면 init 금지 |
| T06 접수 차단·init | 기존 #194 MAINTENANCE에서 closed 전파/모든 serving Pod·전체 기수락/UNKNOWN 확인, 새 typed restriction 승인·고정 S3 version → init PAUSE_NEW | 기존 local Worker는 별도 정산. init APPLIED는 DB 제한 receipt일 뿐 모든 Pod 정지/Fencing 증거 아님 |
| T07 후보 opt-in | 고정 후보 digest + postgres/APPROVED_CONTROL + exact 초기 binding, 새 Pod UID/BootID로 Plan/Policy SHA 재작성 | closed만으로 APPROVED_CONTROL Worker는 멈추지 않음. durable 상태·전체 Pod 반영을 함께 확인 |
| T08 Pause/inspect | 새 요청/서명/TTL로 exact binding Pause, CAS·INTENT/APPLIED·각 Pod claim/dispatch/drain 확인 | INTENT/UNKNOWN은 조회·수동 정산만. 자동 재Apply/재생성/epoch 초기화 금지 |
| T09 재점검/재개 | fresh LIVE Collector 및 외부 SafetyAttestation 원본·TTL, 새 승인 → Resume | 공통5조건 PASS 필수; ACTIVE는 WorkReconciled PASS도 필요. 근거 없는 SingleWriter/Fencing/Provider PASS 금지 |
| T10 새 binding·복귀 | APPLIED 후 새 revision/epoch binding으로 명시적 재배포·접수 재개, 전체 Pod/업무 결과·잔여·Argo 확인 | APPLIED가 기존 Pod latch/file을 자동 해제하지 않음. 이상/불명 결과면 paused 후보 유지·HOLD; journal 삭제 금지 |

T01–T05를 충족하기 전 init/Pause/Resume나 durable opt-in을 실행하지 않는다. 새 시험의 actual UTC 창·
CommandID·ApprovalRef·S3 VersionId·Pod UID/BootID·revision/epoch는 지금 꾸며 채우지 않는다.
계획 JSON의 `EnableExecution=false`, `live_pause_resume_ready=false`를 유지한다.

## 실패 시 관측과 기대 결과

| 조건 | 기대 행동·Evidence |
| --- | --- |
| 신원/권한/대상/승인 purpose/TTL/서명 불일치 | 신규 INTENT/Apply 금지. 비민감 reason과 exact 요청 hash 기록 |
| Head/Get region·owner·version·size·SHA 불일치, callback timeout | 원본 NOT_VERIFIED/HOLD. 다른 endpoint 자동 탐색·mutation 재전송 금지 |
| Pod 집합/UID/BootID/image/restart/Plan hash 변경 | 이전 관측 재사용 금지. 현재 전수 inventory 후 새 Plan/Policy 승인 |
| 유효한 DB 미정산6종 중 양수 | 외부 Safety 부재와 무관하게 WorkReconciled FAIL 보존 |
| SingleWriter/Fencing/Provider 근거 없음 | 해당 NOT_VERIFIED 유지. RECONCILE_ONLY도 공통5조건·별도 승인 우회 불가 |
| Apply 응답 유실/미완료 INTENT | UNKNOWN/HOLD·원본 receipt 보존·inspect. 자동 재Apply/명령ID 교체 재시도 금지 |
| 배포/제어 이후 서비스 이상 | 신규 접수 closed·compatible paused 상태와 이력 유지. 원인/외부 상태 확인 후 명시적 복귀; 자동 open/old local 전환 금지 |

## 별도로 남은 기능 검증

실제 다중 Pod claim 이후 dispatch 제어, 기수락 작업 drain, committed 재요청 멱등성은
전용 시험 조직/대상·Provider 소유권·정산/정리 조건을 정한 뒤 기존 M194/M196 행별로 검증한다.
유휴 상태나 DB0만으로 처리 중 drain·Provider 완료를 PASS로 만들지 않는다.
Pod 강제 종료·응답 유실·경쟁 정지점 주입은 이번 네트워크 진단에서 수행하지 않았다.

이번에는 실제 승인 발급/서명·S3 업로드·init/Pause/Resume·Worker opt-in·후보 재배포를 하지 않았다.
기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·`execution_gate=HOLD`·`recovery_authorized=false`·
`cloud_verified=false`를 유지한다. 전체 Failover/Failback/DR GO와 PR merge는 별도다.
