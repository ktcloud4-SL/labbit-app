# PR #196 작성자 실측과 독립 QA 인계

2026-11-23~24 KST 관측. 시험 대상 App 소스 `3d00482d5538b248dbd8a70ceb6e1991c3d403a1`, 기준 `753eecbff737409a4c9cc8c542f3c6baa6acf0da`. 이번 커밋의 변경은 이 디렉터리의 Evidence에 한정한다. [Draft PR #196](https://github.com/ktcloud4-SL/labbit-app/pull/196) · [LBT-60 기존 QA](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11272) · [LBT-59 운영 인수](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11274)

[관측값](observations.json)과 [원본 SHA-256 목록](source-sha256.json)을 함께 제공한다. SHA-256 목록의 별칭은 내부 인계 ZIP에서 원본 파일을 찾는 데 사용한다. 계정·리소스 ID, 접속 설정, S3 bucket/key/VersionId, 서명과 인증 원본은 팀 내부 인계 ZIP에 보관한다. 공개 Git 자료는 원본에서 확인한 값만 추린 기록이다.

| 실행 | 작성자 결과 | 범위 |
| --- | --- | --- |
| `aws-trial` 실제 승인·Worker 제어 | 실제 STS 신원과 시험용 키로 승인했다. 격리 오사카 EKS Backend 2개와 임시 RDS에서 init, Resume, 재-Pause가 DB 저널에 각각 APPLIED로 남았다. 정산 근거가 없는 Resume는 NOT_VERIFIED이며 INTENT 부재를 재조회했다. 원본 6건의 지정 버전·크기·SHA-256·AES256을 재조회했다. | ACTIVE 중 Pod별 claim 경계 48/40, Pause 시점 80/76에서 최소 12초간 고정. claim 경계 수는 처리한 Provider 작업 수와 별개다. |
| `aws-trial` Fencing·Single Writer | 임시 서울 RDS 앱 계정의 로그인과 기존 쓰기 연결을 차단했다. 오사카 읽기 복제본의 최종 WAL 재생과 시험 마커를 확인한 뒤 승격했다. 오사카 신규 쓰기 ACK와 두 마커를 새 연결에서 확인했다. 서울 앱 경로는 승격 전·후 각 2회 재접속에 실패했다. | 임시 DB 쌍의 앱 쓰기 경로에 대한 PASS_SCOPED. 임시 RDS·SG·시험 네임스페이스·로컬 자격 증명을 정리했다. 시험 승인 및 관측은 동일 운영 승인 체계의 검증으로 간주하지 않는다. |
| `provider-trial` 실제 KT Cloud Cleanup | 제품 HTTP→Worker→WSS→OpenStack Adapter 경로에서 ACK가 반영된 Item을 확인한 뒤 peer Pause를 적용했다. 해당 작업의 RESULT로 Operation과 Item이 SUCCEEDED, 연결된 서버·볼륨 참조 2개가 DELETED, 활성 Worker 수가 1→0이 됐다. 같은 키 4회는 HTTP 200·동일 Operation, 신규 키 2회는 HTTP 503, 추가 Provider 호출 0건이었다. | 별도 Backend 두 스택과 disposable PostgreSQL을 사용했다. Fixture가 RESULT 반환 시점을 제어했다. 관측 순서는 ACK→Pause→DB 반영이다. KT 내부 DELETE가 Pause 전에 접수된 시점과 eligible backlog 경합은 이 RUN에서 확정하지 않는다. |
| `provider-trial` 정리 | Nova soft delete·terminate 성공, 실제 VM·루트 볼륨의 GET 404와 목록 부재, 시험 Tier 부재를 별도 재조회했다. 기존 서버·볼륨·Tier의 ID 집합은 모두 같았다. | 시험 DB 종료·loopback 포트 닫힘·임시 접속 설정 파일 삭제를 확인했다. 자동 정책으로 종료된 합성 DB 디렉터리가 작업 PC에 남았다. |
| `seoul-fencing` 공유 서울 차단·복구 | Backend 중지와 앱 신규 DB 접속 차단, 식별된 기존 세션 1개 종료, 앱 세션 0 확인. 관리 접근과 오사카 복제가 유지됐고 WAL 위치가 맞았다. 원복 후 기존 Backend 2/2와 앱 TLS 재접속 확인. | 공유 서울 원복 실측. 앱 커밋 마커는 NOT_VERIFIED. DNS 전환과 RDS 승격은 이 RUN 범위 밖이다. |
| `seoul-collector`·`pod-integration` | 실제 서울 2 Pod와 RDS의 제품 Collector 읽기 전용 관측에서 AdmissionBlocked·WorkInventory·PodCoverage PASS. 별도 오사카 Linux/시험 PostgreSQL 통합 7개 PASS. | Collector 외부 안전 근거 미제공으로 SingleWriter·Fencing·WorkReconciled NOT_VERIFIED. Linux 통합의 Provider는 모의이며 실제 KT Cleanup RUN과 구분한다. |

공유 서울 Backend·Web 각 2/2와 서울 원본→오사카 읽기 복제 계보를 재조회했다. 이전 ReplicaLag 정책 FAIL, `REQUIRED_SETTINGS`, `RUNTIME_PROVENANCE`, `SERVICE_FUNCTIONS`, `SINGLE_WRITER` 운영 NOT_VERIFIED 및 `execution_gate=HOLD`, `recovery_authorized=false`, `cloud_verified=false`를 유지한다.

## 증분 QA 요청

1. 실제 승인 원본 6개 고정 버전과 DB 저널에서 APPLIED 3건, 미정산 요청의 INTENT 부재, Pod별 Pause 이후 claim 경계 고정을 확인한다.
2. KT Cleanup의 동일 Operation에서 ACK→Pause→RESULT, committed replay 4건과 추가 작업 0건, 정확 자원 재조회 결과를 대조한다.
3. 실제 서울 Fencing과 임시 RDS 승격의 실행 계보를 각각 확인한다. 임시 시험과 공유 운영의 판정을 구분하고 전체 DR HOLD가 유지되는지 확인한다.
4. [SHA-256 목록](source-sha256.json)을 내부 ZIP과 대조한다. 팀 QA의 인수 판단은 위에 명시한 작성자 실측 범위에 적용한다. 공유 환경의 전체 Failover·Failback, 운영 승인·관측 경로는 [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11274)에서 관리한다.

## 게시 검토

- 요구사항과 기존 제품 코드 검토는 [정확 시험 HEAD 감사](https://github.com/ktcloud4-SL/labbit-app/blob/3d00482d5538b248dbd8a70ceb6e1991c3d403a1/evidence/LBT-60/2026-11-23-db-artifact-validation/AUDIT.md)에서 확인한다. 이 게시에는 제품 코드, 계약, 마이그레이션, 설정 변경이 없다.
- Provider 시험 소스는 정확 HEAD의 제품 코드에 테스트용 Worker timeout과 WSS fixture 변경을 사용했다. 합성 준비 시험과 실제 KT 시험을 각각 실행했다. 초기 실행은 PowerShell 플래그 처리로 시작 전에 실패해 인자를 수정했다. 첫 공유 RDS 조회도 오사카 식별자 불일치로 실패했고 기존 물리 ID와 ARN으로 재조회했다.
- 독립 검토에서 원본 접근 정보·리소스 식별자의 공개 범위와 시험 도구의 결과 불명 시 중복 DELETE 가능성을 확인했다. 이 실행에서 raw fallback은 사용되지 않았고, 자원 최종 상태를 재조회했다. 해당 복구 코드의 동작은 인수 범위 밖이다.
- 공개 관측값과 실행 순서를 내부 인계 ZIP의 원본 및 SHA-256과 대조했다. 이번 게시 검증은 기존 실측 자료를 대상으로 수행했다.
