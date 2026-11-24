# PR196 공통 수집 경로 진단·승인 제어 시험 준비

2026-11-23 KST · LBT-60/205 · HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1` + 미커밋 제품 delta.
main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`. PR196 Draft·미병합. 이번에는 push/merge/Slack 전송 없음.
사용자는 서울 서비스 이용자가 없음을 확인하고 진행을 허용했다. NP만 추가하는1차, NP+SG를 추가하는2차
각각 실행 전에 대상·9090 범위·기존 서비스 영향·정리 방법을 이 채팅에 공지했다.
서버 후보 재배포·durable opt-in·실제 init/Pause/Resume·승인 발급/서명·새 S3 업로드는 하지 않았다.

## 결과와 한계

| 실제 관측 | 결과·원본 |
| --- | --- |
| 기존 API server→Backend9090 | 최초 timeout/NOT_VERIFIED, `readonly-route-*.json` |
| 별도 NP만 허용 | 여전히 실패, root `exact-adapter-probe.json`; 이후 해당 NP 정리 |
| exact control-plane2개 `/32`의 NP + node SG TCP9090 | 두 Pod `/livez` 성공, `network-window-02/http-both-open.json` |
| 기본 MAINTENANCE/local의 status | 두 Pod404, 계약상 예상. 제품 Process는 NOT_VERIFIED 유지 |
| 제품 KubeCLI 전체 namespace 목록 | 두 차례 Snapshot 성공; raw response SHA가 달라도 projection Pod가 일치할 수 있음 |
| NP 제거·SG 유지 구간 | health 성공. conntrack/연결 재사용/전파를 분리하지 않아 NP 불필요 또는 단독 원인으로 단정 금지 |
| 원복 | NP2개·이번 SG2규칙 제거. 원래 NP/SG ingress·egress/Deployment/Argo spec 동일, N=2 Ready |
| 기존 오사카 고정 S3 원본 | 보완 전 서울 endpoint 실패·오사카 성공. 보완 후 서울 Target + ArtifactRegion 오사카로 동일 원본299B·VersionId·SHA 일치 |
| 제품 DB CLI 경로 | Windows→RDS5432 미확인. TCP timeout은 DNS/경로/방화벽 중 확정 원인을 뜻하지 않음 |

원복 증거는 `network-window-02/policy-final-readback.json`, `sg-delete.receipt.json`, `sg-final-readback.json`을 본다.
기존 NP를 바꾸지 않고 시험 소유 별도 NP를 생성했으며 삭제는 생성 UID/current RV를 사전조건으로 사용했다.
SG는 생성 receipt의 exact RuleID·owner/VPC/source/port/description을 대조한 뒤 두 규칙만 철회했다.
현재 임시 경로는 남아 있지 않다. 향후 EKS ENI 교체/IP 재사용을 고려해 당시 `/32`를 영구 승인값으로 재사용하지 않는다.

신규 제품 delta는 `Policy.ArtifactRegion`과 immutable S3 reader의 endpoint 선택뿐이다. 빈 값은 기존 Region을 사용하고
서울/오사카 두 값만 허용한다. STS/Target·Database.Region·원본 owner/version/size/hash·timeout은 그대로다.
`artifact-region-postfix-readonly.json`의 Head1438ms/Get990ms, 합계2430ms는 상위 context8초 Reader probe 결과다.
실제 Resumer callback2초 충족, 승인/관측 consumer의 전체 경로 또는 init/Pause/Resume 성공으로 확대하지 않는다.
`artifact-region-readonly.json`은 보완 전 실제 결과이며 덮어쓰지 않았다.

## 자동 테스트 / 별도 미검증

`test-summary.json`과 raw 로그를 따른다. PASS 수는 parent/subtest를 포함한 Go test 이벤트이며 assertion 수가 아니다.

- 보완 후 영향4패키지 Race297 PASS/0FAIL/0SKIP. 보완 전282와 합산하지 않는다.
- Go 전체4671 PASS/88SKIP/0FAIL. 캐시 재사용 package 및 skip 이름은 JSON에 기록했다.
- `go vet ./...`, `go build ./...`, operator/companion 별도 빌드 성공.
- Windows CRLF 때문에 최초 raw gofmt 목록 검사 exit1. 원본 목록을 보존하고 tracked Go의 canonical LF 기준 검사에서0개 불일치를 확인했다.
- Web typecheck/lint/build 성공. test35파일493개 PASS, jsdom canvas 경고는 원본에 유지.
  PATH에 npm이 없어 package.json과 동일한 Node entrypoint를 직접 실행했다. 테스트는 maxWorkers1이다.
- NP5개·SG5개 Mock PASS, 각각 Python `-O`에서도5개 PASS. 최적화에서도 mutation 안전 guard가 유지되는 범위를 검사했다.
  초기 SG4개 로그와 false/missing Return 보완 후5개 로그를 구분한다. Kubernetes adapter8개 PASS는 Mock이다.
- PostgreSQL integration·Linux 전용 fixture는 이번 S3 endpoint/Policy 변경으로 SQL/schema/OS 제어 의미가 바뀌지 않아
  이전 exact-head 감사만 참조하며 새 실행으로 주장하지 않는다. GitHub CI는 아직 이 로컬 delta를 실행하지 않았다.

## 다음 실행과 재현 자료

[실제 시험 순서·중단/복귀 기준](../../../docs/backend/dr-pr196-approved-control-trial.md)의 T01–T10을 따른다.
`trial-plan.json`은 disabled 준비 자료이며 Runtime Policy로 실행할 수 없다. 실제 작업창/새 Pod UID·BootID/
revision·epoch/승인 UUID·S3 VersionId를 임의로 채우지 않았다.
DB verify-full 경로·새 승인 원본 Writer/prefix·2초 callback 지연·실제 외부 Safety 근거가 선행된다.
durable init 이후에는 old/local Worker 자동 rollback을 금지하고 compatible paused 후보·journal을 보존한다.

`source/`는 실제 helper와 제품 delta의 정확 byte 사본이다. 도구 소스는 제품 배포 파일이 아니다.
재현 시 `.txt` 사본을 `source-manifest.json` 원래 상대경로에 복원한다. 의존 `pr196_rollout.py`와 기존 helper의
고정 kubeconfig/도구·계정·EKS UID는 **그 당시 Windows 환경**이다. 현재 대상이 같다고 가정해 create/cleanup을 재실행하지 않는다.
진단은 read-only부터 새 신원·UID를 대조하며, mutation은 별도 fresh 작업창과 사전 공지 후에만 한다.
키/DSN/인증 캐시/kubeconfig 원문은 포함하지 않았다. CA file은 공개 클러스터 CA이며 Plan은 현재 legacy roster 진단용이다.
권한·credential plugin·임시 운영자 실행 파일의 배포/보호는 별도로 준비해야 한다.

`source-manifest.json`, `binary-manifest.json`, `local-tracked.diff`, `pr-range-inventory.json`, `whole-local-status.txt`로
실행 source·기존 PR 범위·로컬 전체 변경을 연결한다. `prior-artifact-integrity.json`은 이전3개 packet의 원본 무변경을
검증하고 후속 문서만 superseded로 표시한다. 과거 manifest를 다시 쓰지 않았다.
`AUDIT.md`, `independent-review.md`, 최종 `artifact-manifest.json`을 함께 인계한다.

**판정:** 이번 진단·지역 선택 보완만 PASS_SCOPED. 전체 Collector·승인 제어 시험 준비/인수는 미완료.
기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·DR HOLD·recovery_authorized=false·cloud_verified=false 유지.
