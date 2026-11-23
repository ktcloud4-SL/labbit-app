# Stage 3 배포 준비 — 실행하지 않은 체크리스트

2026-11-23 갱신 / App 기준 PR196 `94fe1f6`, main `753eecb`. PR196은 Draft·미병합이다.
11/21 오프라인 준비와 11/23 AWS·Kubernetes 읽기 전용 조회를 구분한다. 이번 확인에서
서울 서비스·실제 DB·AWS 설정·Slack을 변경하지 않았다.

| 순서 | 준비·실제 검증 | 완료 기준 / 중단 |
| --- | --- | --- |
| 1 | #196 리뷰/CI 및 새 operator·Runtime 계약 리뷰 | 미해결 결함·기존 사용자 작업 diff 충돌이면 중단 |
| 2 | 승인된 작업창·대상·관리자·백업 담당 확정 | 데모/다른 시험 중 변경 금지. 데이터/로그·기존 작업 보존 |
| 3 | DB 백업·복원 시험 근거, 현재 migration 목록과 schema 확인 | 000012~000014 **기존 적용 파일 변경 없이** migration runner로 적용할 계획만 검토 |
| 4 | migration 전/후 row·제약·권한·적용 기록 확보 | startup에서 DDL/자동 seed 금지. 임의 down migration/불명 journal 삭제 금지 |
| 5 | 고정 image digest·Git revision·원본 해시 기록 | 후보 image가 실제 build되고 196 schema/config를 포함했는지 검증 |
| 6 | 제어 DB 위치·단일 authoritative Writer·권한 검증 | 리전별 비동기 복제 DB를 각자 독립 control Writer로 사용 금지 |
| 7 | Maintenance closed 및 전체 serving Pod 확인, 작업 Inventory 보존 | UNKNOWN·기수락 작업 누락·Pod coverage 불명은 HOLD |
| 8 | PAUSE_NEW row 초기화/인수, immutable binding으로 제한 배포 | 11/23 초기 관측 당시 init/Pause 진입점 미구현. 하단 오프라인 보완 참조; 배포는 수행하지 않음 |
| 9 | 기존 #194 미검증 + #196 부분 시험 | 실제 drain·committed replay·여러 Pod claim/dispatch·승인 재개·실패 HOLD |
| 10 | 결과·정리·잔여 자원·독립 QA 인수 | 실제 Fencing·Single Writer·DR 실행은 별도 Gate |

실제 DB 연결 문자열·백업 ARN·이미지 digest·작업창·정확한 승인 출처가 없는 현재 문서는 실행 입력이 아니다.
CLI의 DSN은 보호된 파일을 통해 전달하며 로그에 원문을 남기지 않는다. 승인/관측 private key는 전달 자료에
포함하지 않는다. migration 실패·부분 적용·코드 롤백 상황은 실제 migration 원본과 현재 schema를 먼저 조회한다.
데이터를 보존한 채 접수 closed·Worker PAUSE를 유지하고 담당자가 forward-fix/승인 복원 여부를 결정한다.

복원으로 INTENT/승인 소비 이력이 사라지면 이전 명령을 재전송하지 않는다. 별도 외부 원본과 실제 Provider 작업,
epoch·Writer·불명 결과를 재대조하기 전 재개하지 않는다. 예전 image로 돌아가 durable 제어를 잃는 rollback도
자동 허용하지 않는다. 보존 기간 중 제어 journal 삭제/TTL 정리는 하지 않는다.

남은 #194 drain/committed replay는 #196 후보의 회귀·부분 시험 Plan에 포함하되 #194 코드 인수와 #196 추가
기능 인수를 구분한다. 11/21 준비 당시에는 AWS가 내려가 있어 배포·실제 migration·원격 PostgreSQL·Pod 시험을
실행하지 않았다. 11/23 현재 상태와 다음 변경 경계는 아래를 따른다. 과거 CI/Mock PASS를 현재 서비스 PASS로 옮기지 않는다.

## 11/23 읽기 전용 준비 확인

[실측·한계·실행 전 확인 목록](../../evidence/LBT-60/2026-11-23-live-readiness/readiness.json).
관측은 당시 스냅샷이다. 실행 직전에 다시 조회하고, 이전 PASS를 승인으로 재사용하지 않는다.

- 서울: Backend 2/2, image `sha-753eecb`, digest
  `sha256:bab1869ef211ddc0f1a559e4c9d543d5add1d59269e5db85aad2e384bed42419`.
  역할은 `api,realtime,worker,preview`; maintenance ConfigMap의 `mode=open` 선언을 확인했다.
  Pod 내부 mount 값·프로세스 제어 상태는 이번에 읽지 않았다. durable 제어 환경 변수는 미설정이다.
- 서울 migration Job은 성공했으며 로그 요약은 `applied_count=11`, `latest_known_version=11`이다.
  현재 DB의 schema/checksum을 직접 읽은 결과가 아니다. 후보 migration12~14의 적용·실제 복원 시험은 미실행이다.
- RDS는 서울 available, 오사카 available/replicating을 관측했다. 오사카는 10:08 KST 복제 중단 후
  10:10 KST 재개 이벤트가 있다. CloudWatch 지연 지표와 재기동 이력을 보존하며 업무 RPO·Single Writer로 해석하지 않는다.
- 오사카 EKS는 조회 가능하지만 당시 `labbit` Deployment/Pod는 없었고, Argo Deployment와 Application CRD도 없었다.
  이는 관측 당시 구성이다. 서울의 #196 부분 시험을 위해 오사카 앱을 먼저 켤 필요는 없다.
- ECR의 정확한 `sha-94fe1f6` 태그 조회는 ImageNotFound였다. 다른 저장소·태그·이미지 전체의 부재를 입증한 것은 아니다.
  기존 PR workflow는 image `push:false`이므로 merge 없이 시험할 후보 게시·고정 digest 경로를 별도로 준비해야 한다.
- CLI 오프라인 54개 테스트와 실제 빌드 실행 파일의 preview/비활성 실행을 확인했다. 별도 allowlist helper로
  기존 AWS adapter의 STS 신원·서울 RDS metadata 일치 및 잘못된 ResourceId 거부도 실측했다(STS1회+RDS2회).
  helper의 trust 필드는 합성이다. 실제 운영 Policy·서명·S3·SQL·Pause/Resume·Single Writer 검증이 아니다.

## 다음 준비와 서비스 영향 경계

1. 별도 시험 RUN·대상·작업창·실행자/QA를 고정하고, 현재 이용자·작업/UNKNOWN inventory를 확인한다.
2. 실제 Pause/init 조작 경로, 보호된 Policy·목적별 서명키, 승인/관측 원본 경로와 전체 Pod 수집·동결 절차를 준비한다.
   Resume CLI 후보는 구현되어 있지만 실제 안전 조건 수집·키 운영·Pause를 제공하지 않는다.
3. schema/checksum·백업/복귀 근거와 후보 digest를 확인해 migration/설정/이미지 변경안을 먼저 공유한다.
   공유 DB DDL, PreSync Job, serving Pod 교체, maintenance closed·Worker Pause는 서비스 영향이 있으므로 이 조회에서 실행하지 않았다.
4. 선행 조건이 확보된 행부터 #194/#196 부분 시험을 실행한다. 미준비 행은 NOT_VERIFIED로 남긴다.
   Resume는 계약의 여섯 안전 조건을 실제로 입증한 경우에만 별도로 실행한다. 합성 PASS로 우회하지 않는다.
5. 원래 허용된 설정·전체 Pod·미해결 작업·잔여 자원을 대조하고 독립 QA에 인계한다.

서울 부분 시험과 전체 DR 전환을 구분한다. 오사카 앱/Argo 활성화, ReplicaLag15초 충족, DB 승격,
DNS 변경, 22단계 Failover/Failback 전체를 서울 부분 시험의 일괄 선행 조건으로 추가하지 않는다.
반대로 부분 시험 PASS로 실제 Writer Fencing·Single Writer·전체 DR Gate를 해제하지 않는다.

현재 ReplicaLag 정책 FAIL·네 가지 NOT_VERIFIED·`execution_gate=HOLD`·`recovery_authorized=false`·
`cloud_verified=false`를 유지한다. 이번 증분은 읽기 전용 근거와 준비 문서이며 배포·merge 승인이 아니다.


## 2026-11-23 로컬 연결 보완 (미배포)

같은 live-prep 브랜치에서 Runtime 0.1.14 후보를 구현했다. 앞 절의 읽기 전용 readiness는 당시 상태로 보존한다.

1. `labbit-dr-control`의 승인된 init/Pause 및 inspect-restriction을 추가했다. migration15는 별도 적용 선행조건이다.
2. `labbit-dr-evidence collect`는 고정 Plan·TLS CA·전체 Pod 조회·실제 BootID 및 read-only SQL inventory를 연결한다.
   외부 Fencing/SingleWriter/Provider 원본이 없으면 NOT_VERIFIED다. 임의 PASS 입력이나 Worker OFF로 대체하지 않는다.
3. `labbit-dr-evidence approve`와 관측 서명은 OS 보호 PKCS8 key와 Policy 목적·공개키·Phase를 맞춘다.

배포 전 migration15 백업/적용/호환성, 고정 후보 이미지, Admin/RBAC, 운영 키·Policy·관측/승인 보관,
작업창·동결 범위 및 실제 전체 Pod/기수락/UNKNOWN 정산을 확인해야 한다.
이번 구현 중 AWS/서울/공유 DB 조회·변경·Slack·push는 하지 않았다. 폐기용 로컬 PostgreSQL은 구분해 검증한다.
전체 DR HOLD와 기존 FAIL/4NOT_VERIFIED는 유지한다.
