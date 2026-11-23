# Stage 3 배포 준비 — 실행하지 않은 체크리스트

2026-11-21 / App 기준 PR196 `c11b70a`, main `753eecb`. 이번 후보는 별도 local branch다.
서울/실제 DB/AWS/Slack 변경은 없다. PR196은 Draft이며 배포·merge 상태를 바꾸지 않는다.

| 순서 | 준비·실제 검증 | 완료 기준 / 중단 |
| --- | --- | --- |
| 1 | #196 리뷰/CI 및 새 operator·Runtime 계약 리뷰 | 미해결 결함·기존 사용자 작업 diff 충돌이면 중단 |
| 2 | 승인된 작업창·대상·관리자·백업 담당 확정 | 데모/다른 시험 중 변경 금지. 데이터/로그·기존 작업 보존 |
| 3 | DB 백업·복원 시험 근거, 현재 migration 목록과 schema 확인 | 000012~000014 **기존 적용 파일 변경 없이** migration runner로 적용할 계획만 검토 |
| 4 | migration 전/후 row·제약·권한·적용 기록 확보 | startup에서 DDL/자동 seed 금지. 임의 down migration/불명 journal 삭제 금지 |
| 5 | 고정 image digest·Git revision·원본 해시 기록 | 후보 image가 실제 build되고 196 schema/config를 포함했는지 검증 |
| 6 | 제어 DB 위치·단일 authoritative Writer·권한 검증 | 리전별 비동기 복제 DB를 각자 독립 control Writer로 사용 금지 |
| 7 | Maintenance closed 및 전체 serving Pod 확인, 작업 Inventory 보존 | UNKNOWN·기수락 작업 누락·Pod coverage 불명은 HOLD |
| 8 | PAUSE_NEW row 초기화/인수, immutable binding으로 제한 배포 | 이 CLI는 init/Pause/배포를 수행하지 않음. 운영 진입점 추가 검증 필요 |
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
기능 인수를 구분한다. 현재 AWS가 내려가 있어 배포·실제 migration·원격 PostgreSQL·Pod 시험은 실행하지 않았다.
