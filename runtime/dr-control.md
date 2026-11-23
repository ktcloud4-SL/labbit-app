# DR Worker 제어 운영 경계 v1 — opt-in 후보

Owning Runtime 0.1.13 / LBT-59·60·205. `cmd/labbit-dr-control`은 별도 실행 파일이다.
기본 preview에서는 JSON을 검사할 뿐 AWS·DB·S3·서비스에 접속하지 않는다. `--execute`와
보호된 Policy의 `EnableExecution=true`가 모두 있어야 실제 adapter 경로에 진입한다.
현재 실제 배포·승인 시스템·실환경 재점검 인수는 **미완료**다.

## 입력과 권한

`--policy <보호된 JSON> --input <고정 JSON>`을 사용한다. 실행 모드에서만 `--dsn-file`이 필요하다.
CLI 인자·출력에 DSN/키/토큰을 넣지 않는다. JSON 대소문자 alias 중복·중복 key·알 수 없는 field·
중복 문서·비정상 UTF-8·256KiB 초과를 거절한다. 필드 정의 원본은 `internal/drcontrol/contract.go`.
임의 endpoint/SQL/shell/callback 경로를 입력받지 않는다.

Policy에는 다음을 모두 명시한다. 기본 승인 TTL이나 운영자 allow-all은 없다.

- `Version=labbit-dr-control/v1`, `AccountID`, `Region`, 정확한 `Target`(ID/Region/Cluster).
- `AWSExecutable` 절대경로·`AWSProfile`. AWS 실행 파일과 profile/config/credential_process는
  trusted operator 소유다. 서비스 계정이 수정 가능한 파일·PATH 기반 실행은 허용하지 않는다.
- `Actors`: exact ARN, 내부 Principal, 허용 Action(`resume`, `inspect`). assumed-role은 세션 이름까지
  exact ARN으로 고정하며 광범위한 account/role wildcard는 없다. 역할 세션 변경 시 Policy 재검토 필요.
- `ApprovalKeys`, `ObservationKeys`: Key ID·Principal·base64 Ed25519 public key·허용 Phase.
  두 목적은 서로 다른 공개키 바이트를 요구한다. private key는 코드·예제·Git에 보관하지 않는다.
- `ArtifactBucket/ArtifactPrefix`, `ApprovalMaxSeconds/EvidenceMaxSeconds`의 명시적 정책.
  1..86400초는 입력 상한일 뿐 운영 권고가 아니다. 실제 정책·시계 신뢰·변경 동결은 별도 확정한다.
- `Database`: 정확한 RDS ARN·DbiResourceId·리전·Host/Port/Name/User. 동일 account의 다른 DB도 거절한다.

모든 호출은 STS 신원을 검사한다. 기존 APPLIED/INTENT의 조회와 inspect도 인증 전에 journal에 접근하지 않는다.
inspect는 역사적 receipt 조회이며 입력은 원래 ResumeRequest 그대로다. 새 상태 활성화·재배포를 수행하지 않는다.

## 승인·관측의 비순환 binding

1. Observation은 `EvidenceBinding`에 명령·Scope·revision/epoch·Desired·Trial/RUN/Phase·Principal·
   ApprovalRef·PlanSHA256·RequestedAt/ValidUntil을 포함한다. **자기 자신 SHA는 포함하지 않는다.**
2. Observation을 별도 관측 key로 서명해 고정 버전으로 보관하고 raw document SHA를 계산한다.
3. 최종 ResumeRequest의 `PreconditionsSHA256`을 그 raw SHA로 설정한다.
4. Approval은 최종 Request.SHA256·UUID ID(`ApprovalRef`와 일치)·Principal·작업창을 승인 key로 서명한다.
5. Invocation은 승인/관측의 Bucket·Key·VersionID·SHA256·Size를 고정한다. latest/null Version은 불가다.

Signed envelope는 `Version`, `KeyID`, `Payload`, `Signature`다. Ed25519 message는
`labbit-dr-control/v1/<approval 또는 observation>\n` + **Payload의 정확한 JSON 바이트**다.
서명 뒤 Payload를 pretty-print·정렬·재직렬화하면 서명이 무효다. Envelope와 원본을 그대로 보관한다.
키 purpose/Phase, 원본 크기·SHA·고정 버전, 최종 요청 binding을 모두 확인한다.

Approval은 동일 Principal/ID/요청과 `[NotBefore, ExpiresAt)`를 요구한다. Observation은
`Source=LIVE_READBACK`, 동일 EvidenceBinding, `RequestedAt <= ObservedAt <= now < ValidUntil`,
명시 TTL을 요구한다. SIMULATION·미래/만료·FAIL·NOT_VERIFIED·누락 안전 조건은 재개를 막는다.
AdmissionBlocked/SingleWriter/Fencing/WorkInventory/PodCoverage는 PASS 필수이며 ACTIVE에는
WorkReconciled PASS도 필요하다. 이 입력은 **서명자가 실제 수집·검증했다는 신뢰 경계**다.
현재 코드가 클러스터 전체 Pod·Writer 격리·업무 정산을 직접 입증하지 않는다.

## 실행·실패·책임

AWS adapter는 STS GetCallerIdentity, 고정 버전 S3 Head/Get, RDS Describe만 수행한다.
공식 endpoint를 고정하고 shell을 사용하지 않는다. SDK default retry 대신 AWS CLI max attempts1,
호출 context2초·CLI connect/read1초를 적용한다. 기존 Resumer의 전체 callback2초보다 늦으면 HOLD다.
Head·Get 2회가 포함되므로 실환경 지연/인증 갱신 동작은 별도 측정해야 한다.
artifact 원본은 exclusive 임시파일에 수집하고 원본 hash 확인 후 정확한 파일만 정리한다.

RDS exact ARN/ResourceId/endpoint/available/postgres를 확인한 뒤, DSN은 TLS hostname/CA 검증과
exact Host/Port/DB/User·단일 호스트를 요구한다. 연결 후 `pg_is_in_recovery=false`, DB/user를 확인한다.
이는 Single Writer 증거를 대신하지 않는다. 자동 migration/init/DDL/Pause/삭제는 없다.

서명된 안전 조건과 authoritative Store의 CAS/drain 검증을 통과한 명령만 기존 INTENT→Apply 경로로 전달한다.
일반 출력은 비민감 reason code와 CommandID/RequestSHA, journal 상태만 제공한다.
INTENT 후 timeout/응답 유실은 UNKNOWN/HOLD로 수동 조사하며 mutation을 재전송하지 않는다.
APPLIED는 역사적 명령 결과다. 기존 process의 pause latch·immutable binding·maintenance 파일을 열지 않는다.

운영 도구는 Pod 내 local Gate를 직접 읽지 않는다. `NewMaintenanceResumer`의 local 확인과 같은 보장을
주장하지 않으며, 외부 PodCoverage/AdmissionBlocked 수집과 변경 동결이 필수다. 제어 DB scope CAS는
리전 간 전역 Failover/Failback 실행 잠금이 아니다. 아직 해당 lock·수집자·키 배포/회전·운영 Pause
진입점·실환경 IAM/PG/전체 Pod 시험이 남아 있다. 이 후보를 배포 승인으로 사용하지 않는다.

기존 ReplicaLag FAIL·네 가지 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·
cloud_verified=false는 모든 CLI 결과에서 유지한다. 이 도구의 APPLIED와 전체 DR GO는 별개다.

공식 AWS 명령 근거: [S3 고정 버전 조회](https://docs.aws.amazon.com/cli/latest/reference/s3api/get-object.html),
[RDS 식별 조회](https://docs.aws.amazon.com/cli/latest/reference/rds/describe-db-instances.html).
