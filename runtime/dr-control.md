# DR Worker 제어 운영 경계 v1 — opt-in 후보

Owning Runtime 0.1.15 / LBT-59·60·205. `cmd/labbit-dr-control`은 별도 실행 파일이다.
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
- `Actors`: exact ARN, 내부 Principal, 허용 Action(`init`, `pause`, `inspect-restriction`, `resume`, `inspect`, `collect`). assumed-role은 세션 이름까지
  exact ARN으로 고정하며 광범위한 account/role wildcard는 없다. 역할 세션 변경 시 Policy 재검토 필요.
- `ApprovalKeys`, `ObservationKeys`: Key ID·Principal·base64 Ed25519 public key·허용 Phase.
  두 목적은 서로 다른 공개키 바이트를 요구한다. private key는 코드·예제·Git에 보관하지 않는다.
- `ArtifactBucket/ArtifactPrefix`, `ApprovalMaxSeconds/EvidenceMaxSeconds`의 명시적 정책.
  1..86400초는 입력 상한일 뿐 운영 권고가 아니다. 실제 정책·시계 신뢰·변경 동결은 별도 확정한다.
- 선택적 `ArtifactRegion`은 고정 버전 S3 Head/Get의 보관 리전이다. 허용 값은
  `ap-northeast-2` 또는 `ap-northeast-3`이며 생략하면 기존 `Region`을 사용한다.
  서울 제어·오사카 Evidence처럼 리전이 다르면 보호된 Policy에 명시한다. STS/Target의
  제어 리전과 `Database.Region`은 바꾸지 않으며, 자동 리전 검색·실패 후 다른 리전 재시도는 없다.
  Bucket/Prefix·owner·VersionID·크기·SHA 검증 및 권한 범위도 그대로 유지된다.
- `Database`: 정확한 RDS ARN·DbiResourceId·리전·Host/Port/Name/User. 동일 account의 다른 DB도 거절한다.
- 선택적 `DatabaseTunnelPort`: 0/생략은 기존 직접 연결, 1024..65535는 정확한
  `127.0.0.1:<port>` TCP 운송만 사용한다. DSN의 Host/Port/DB/User와 TLS ServerName·CA 검증은
  실제 RDS 대상을 그대로 요구한다. localhost DSN·TLS 생략·다른 host/network fallback은 없다.
  터널은 도구가 만들지 않는다. 운영자가 exact SSM target/document/remote RDS host·5432와
  로컬 포트 독점·세션 수명주기를 확인해야 한다. 로컬 포트 연결만으로 RDS 신원을 인정하지 않는다.

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
관측 수집 경로는 아래와 같다. 클러스터 scope 밖 경로·실제 Writer 격리·Provider 정산의 사실 검증은 별도 신뢰 수집자가 담당한다.

## 실행·실패·책임

AWS adapter는 STS GetCallerIdentity, 고정 버전 S3 Head/Get, RDS Describe만 수행한다.
공식 endpoint를 고정하고 shell을 사용하지 않는다. SDK default retry 대신 AWS CLI max attempts1,
호출 context2초·CLI connect/read1초를 적용한다. 기존 Resumer의 callback2초는 유지한다.
새 init/Pause/Resume는 인증→역사적 journal 조회 후에만 승인/관측의 정확한 고정 버전 바이트를
최대 8초 안에 준비한다. Resume는 2개, 제한 명령은 승인1개다. 전체 CLI15초·현재 TTL을 연장하지 않는다.
준비 중 만료·원본 불일치·조회 실패면 신규 INTENT를 만들지 않는다. 준비 뒤 내부 Inspect/CAS로
경쟁 명령을 다시 확인하며 과거 APPLIED/INTENT·inspect는 원본을 다시 내려받지 않는다.
2초 콜백에서는 요청별 바이트 사본의 signature/purpose·request·대상·**현재** TTL을 다시 판정한다.
서명/TTL/신원/GO 결과를 캐시하지 않으며 호출 종료 시 바이트를 비운다. 누락 시 네트워크 fallback도 없다.
다운로드 뒤 IAM 회수나 객체 삭제 여부는 이 invocation의 콜백에서 재조회하지 않는다는 한계가 있다.
긴급 폐기는 보호된 Policy의 key/Actor 제거·실행 opt-in 중지와 별도 운영 통제로 처리한다.
이미 시작한 invocation은 Policy도 snapshot이므로 파일 변경만으로 즉시 회수되지 않는다.
즉시 중단은 운영 프로세스·DB 접근 등 별도 통제를 사용하고, INTENT 유무를 먼저 조사한다.
이 바이트 snapshot 의미를 수용할 수 없는 시험은 실행하지 않는다. AWS 지연/인증 갱신은 별도 관측한다.
artifact 원본은 exclusive 임시파일에 수집하고 원본 hash 확인 후 정확한 파일만 정리한다.

RDS exact ARN/ResourceId/endpoint/available/postgres를 확인한 뒤, DSN은 TLS hostname/CA 검증과
exact Host/Port/DB/User·단일 호스트를 요구한다. 연결 후 `pg_is_in_recovery=false`, DB/user를 확인한다.
이는 Single Writer 증거를 대신하지 않는다. 자동 migration/DDL/삭제는 없다. init/Pause는 별도 명시 승인 명령만 가능하다.

서명된 안전 조건과 authoritative Store의 CAS/drain 검증을 통과한 명령만 기존 INTENT→Apply 경로로 전달한다.
일반 출력은 비민감 reason code와 CommandID/RequestSHA, journal 상태만 제공한다.
INTENT 후 timeout/응답 유실은 UNKNOWN/HOLD로 수동 조사하며 mutation을 재전송하지 않는다.
APPLIED는 역사적 명령 결과다. 기존 process의 pause latch·immutable binding·maintenance 파일을 열지 않는다.

## 명시 승인된 init / Pause

`labbit-dr-control --policy <path> --input <path>`는 `RestrictInvocation`도 지원한다.
Action은 `init`, `pause`, `inspect-restriction`이며 입력 Request.Action은 원래 `init` 또는 `pause`다.
init은 Scope와 zero Binding, pause는 실제 프로세스에서 관측한 exact Binding을 요구한다.
Trial/RUN/Phase·Principal·PlanSHA·CommandID·ApprovalRef·UTC 작업창을 고정한다.
`restriction-approval` domain의 서명 Approval을 고정 S3 VersionID·SHA·Size로 참조한다.
Resume 승인·관측 서명을 재사용할 수 없으며 init/Pause도 exact STS Actor/Action을 확인한다.

`worker_control_restrictions`(migration15)는 full 요청/승인과 INTENT를 먼저 commit한다.
생성 승자만 Apply를 1회 시도하며 기존 INTENT는 UNKNOWN/HOLD로 조회만 한다.
상태 변경과 APPLIED는 같은 transaction이다. init은 기존 Scope를 재설정하지 않고,
pause는 기존 state/epoch/revision을 CAS해 PAUSE_NEW로 제한하며 활성화하지 않는다.
이미 PAUSED여도 exact 명령 receipt를 보존한다. pending restriction은 Resume와 Drained를 막는다.
과거 승인 만료는 현재 신원으로 수행하는 역사적 receipt 조회를 막지 않는다.

이 명령의 APPLIED는 제어 DB의 제한 변경 receipt다. maintenance 파일 closed 적용,
모든 Pod 반영·기수락 작업 drain·Provider 정산·Fencing/Single Writer까지 보장하지 않는다.
잠금 없는 외부 작업은 별도로 정산해야 한다. UNKNOWN을 삭제하거나 자동 재Apply하지 않는다.

## 서명·수집 companion

`labbit-dr-evidence --mode approve|collect --policy <path> --input <path>`도 기본 preview다.
preview는 key/DSN/kubeconfig를 읽지 않고 외부 호출하지 않는다. `--execute`와 Policy opt-in이 필요하다.
출력은 signed 원본과 제한된 report이며 S3 업로드나 제어 변경을 수행하지 않는다.

- approve: 실제 STS Actor를 확인한 뒤 exact init/Pause/Resume 요청의 승인 문서를 만든다.
  `--key-id`, `--key-file`의 PKCS8 Ed25519 key가 Policy 공개키·Principal·Phase·purpose와 일치해야 한다.
- collect: Policy `CollectionPlanSHA256`과 일치하는 보호된 `--plan`을 요구한다.
  실행 Actor의 collect 권한과 Observation Key Principal도 일치해야 한다.
  `--dsn-file`, `--key-id`, `--key-file`을 지정하며 `--safety-reference`는 선택적 immutable Reference다.
- 개인키·실행 Policy·DSN은 Unix current owner/other permission0, Windows current owner와
  Owner/System/Administrators 이외 Allow ACE가 없는 DACL을 요구한다. symlink·oversize·상대경로를 거절한다.
  부모 디렉터리·실행 파일·AWS/kube credential plugin도 trusted operator만 수정할 수 있어야 한다.
  키 생성/배포/회전은 하지 않는다. Policy에서 제거한 KeyID는 사용할 수 없다. 키 본문은 출력하지 않는다.
- 서명은 typed payload를 compact JSON으로 만든 정확한 바이트에 적용한다. 서명 후 재직렬화하지 않는다.
  승인/관측 key는 분리된다. `safety-attestation`도 별도 domain이며 임의 JSON 승인 우회가 아니다.

## 실제 읽기 전용 collector와 안전 경계

CollectionPlan은 고정 kubectl/kubeconfig/context/API server/kube-system UID/namespace,
CAFile/CASHA256, expected Pod name/UID/container/image digest와 명시적 제외 name/UID를 포함한다.
전체 namespace Pod 목록을 페이지 끝까지 읽고 모든 Pod를 계획과 대조한다. 비어 있는 발견 목록은 PASS가 아니다.
조회 전후 UID/resourceVersion/restart/image/Ready가 달라지면 PodCoverage는 NOT_VERIFIED다.
API endpoint를 고정하고 TLS 생략=false·서버 hostname·고정 CA를 명시하며 shell/exec/apply는 하지 않는다.

persistent control을 명시한 Backend의 **비공개 Admin listener**에만 `GET /dr-control/status`가 열린다.
public/peer listener에는 route가 없고 POST는 거절한다. 실제 프로세스 Binding/BootID, local snapshot,
exact closed 및 http/worker/reconciliation count, UTC를 반환한다. DB·Secret·외부 요청은 없다.
기본 maintenance-only 구성에는 경로가 없다(404). Admin 9090과 pods/proxy는 운영자 RBAC로 제한해야 한다.
MaintenanceState는 exact open/closed/NOT_VERIFIED와 같은 잠금에서 관측한 전체 count를 구분한다.
잘못된 파일·필수 count 누락·음수·Provider 근거 미확인은 NOT_VERIFIED이며,
확인된 open·양수 미정산 count·검증된 FAIL만 FAIL이다. 잘못된 파일의 admission 차단을 closed PASS로 인정하지 않는다.

SQLWorkReader는 exact RDS/DSN/TLS/SQL 신원 확인 뒤 read-only repeatable-read transaction으로
DB 전체의 Operation/Item/lease, Provider 미해결, 모든 control boundary·Resume/Restriction INTENT를 센다.
명령 allowlist나 현재 Pod의 작업으로만 범위를 줄이지 않는다. WorkInventory PASS는 목록 조회에 한정되며,
카운트0만으로 Provider 작업 완료·SingleWriter를 추정하지 않는다.
신원·대상·revision/epoch·시각·비음수 카운트가 검증된 DB 조회에서 미정산 카운트가 하나라도 양수이면,
외부 SafetyAttestation이 없거나 유효하지 않아도 WorkReconciled는 FAIL이다. 카운트가 모두 0이어도
외부 Provider 정산 근거 없이는 NOT_VERIFIED이며, DB 조회 자체가 불확실하면 양수 값을 신뢰하지 않는다.

SingleWriter/Fencing/Provider 정산에는 별도의 trusted collector가 발행한 `SafetyAttestation`이 필요하다.
이는 Version/Source=LIVE_READBACK, exact EvidenceBinding/DBResourceID/UTC TTL, 세 판정과
고정 VersionID·SHA·Size의 원본 Sources를 요구한다. 원본은 승인된 Artifact 경로에서 재조회한다.
이 구현은 외부 Fencing 자체를 수행하거나 원본의 업무 사실을 자동 입증하지 않는다. 서명자의 관측 책임이 남는다.
근거 없으면 NOT_VERIFIED, 독립적으로 확인된 FAIL은 다른 조회 실패에도 보존한다.

전체 Pod 목록·동결 범위·scope 밖 API/Job·관리 경로·네트워크 차단·이전 Writer 재시작 통제는 실환경에서
별도 확인해야 한다. 현재 collector의 metadata PASS나 서명은 DR GO가 아니다.
관측 보고서/원본 SHA와 signed 문서를 함께 승인된 보관처로 인계해야 하며 이번 CLI는 업로드하지 않는다.
키 운영·공유 DB migration15·RBAC·다중 Pod·외부 Provider/Fencing·실제 Pause/Resume는 미검증이다.
제어 DB Scope CAS는 리전 간 전역 Failover/Failback 잠금이 아니다.

기존 ReplicaLag FAIL·네 가지 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·
cloud_verified=false는 모든 CLI 결과에서 유지한다. 이 도구의 APPLIED와 전체 DR GO는 별개다.

## 승인 원본 보관용 별도 운영 도구

`scripts/dr_control_artifact_store.py --plan <JSON> --source <absolute-public-signed.json>
--receipt <absolute-receipt.json>`는 별도 도구이며 기본 preview다. `--execute`와 Plan의
`enable_upload=true`, exact STS user/account, 최대1시간 UTC 작업창이 모두 필요하다.
`labbit-dr-control`/`labbit-dr-evidence` 자체는 계속 S3를 쓰지 않는다.
Plan은 고정 bucket/prefix/key/region/owner·원본 크기/SHA·보존 검토 UTC를 명시한다.
공개 Signed envelope의 shape만 검사한다. 승인 권한·서명·purpose·TTL의 수락은 제어 consumer 책임이다.
개인키·DSN·Secret은 입력하지 않는다. 공개 원본을 재직렬화하지 않고 정확한 바이트를 저장한다.

운영자가 신뢰할 수 있는 로컬 영속 디렉터리·소유자 ACL을 먼저 준비한다. Plan/source/receipt의 부모와
실행 파일은 같은 신뢰 경계다. receipt lock은 exclusive이며 crash 후 남으면 수동 조사한다.
fsync한 INTENT→PUT 직전 작업창 재검사→`If-None-Match:*`·AES256·전송 checksum으로 1회 생성→
VersionId 기록→지정 버전 재조회와 정확한 바이트 비교 순서다. PUT 응답 불명/metadata 오류는 UNKNOWN/HOLD다.
기존 INTENT/UNKNOWN은 PUT하지 않는다. STORED/READBACK_VERIFIED는 **기록된 버전만 재조회**할 수 있다.
receipt/lock 삭제·새 Key로 변경하는 것은 불명 PUT을 재시도할 근거가 아니다.
원본/receipt 손상·로컬 저장장치의 완전한 전원 손실 durability는 별도 운영 위험이며 자동 재시도하지 않는다.

보존 기간은 작업창 종료보다 최소30일 이후로 명시하지만 lifecycle/Object Lock을 설정하거나 삭제하지 않는다.
수동 보존 담당·비용·기한·독립 Reader 인수는 별도다. 버전관리·조건부 생성은 관리자 삭제 방지가 아니다.
11/23 실측은 만료·SIMULATION 샘플2건의 저장/읽기 범위이며 실제 운영 승인·전체 DR 인수가 아니다.

공식 AWS 명령 근거: [S3 고정 버전 조회](https://docs.aws.amazon.com/cli/latest/reference/s3api/get-object.html),
[RDS 식별 조회](https://docs.aws.amazon.com/cli/latest/reference/rds/describe-db-instances.html).
