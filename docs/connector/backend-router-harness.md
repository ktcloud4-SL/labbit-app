# LBT-167 Backend Router 단발 Harness

이 경계는 C1의 수동 통합 시험용이다. Cloud 실행·배포 승인은 별도이며 이 PR에서 실제 실행하지 않는다. 제품 HTTP/DB/Runtime 계약과 durable Operation 처리는 바꾸지 않는다. wire와 Provider 의미는 [Connector 계약](../../contracts/connector/README.md), 고객 환경 설정은 [Runtime 계약](../../runtime/contract.yaml)을 따른다.

## 로컬 Mock 검증

```bash
go test ./internal/server/app ./internal/server/connector
go vet -tags 'backend_router_harness integration' ./internal/server/app ./internal/server/connector
go build -tags backend_router_harness -o /tmp/labbit-c1-server ./cmd/labbit-server
LABBIT_TEST_DATABASE_DSN='<폐기용 로컬 PostgreSQL test DSN>' \
  go test -race -tags 'backend_router_harness integration' -run '^TestC1' -count=1 ./internal/server/app
```

tagged 테스트는 Mock Provider만 사용한다. 실제 KT acceptance test를 실행하거나 서버 binary를 시작하지 않는다. PostgreSQL test DB는 기존 `postgrestest`가 격리·삭제한다. CI도 위 Mock 검증만 실행한다.

일반 `go build ./cmd/labbit-server`와 Dockerfile에는 `backend_router_harness` tag가 없다. `router_harness.go`와 `router_harness_plan.go`는 제외되고 default hook은 환경변수·파일 조회·goroutine·Sink 설치를 하지 않는다. tagged build도 `LABBIT_C1_HARNESS=WAIT_FOR_OPERATOR`가 없으면 같은 no-op이다.

## 후속 승인 이후의 실행 준비

Linux 시험용 Backend artifact를 명시적으로 tag로 빌드한다. 기존 `app.Run`의 application listener, DB 인증, `newControlStack`, WSS Handler, Registry, Router를 그대로 사용한다. 별도 Router process, Mock SaaS, 신규 Connector credential, 공개/debug endpoint는 없다. 일반 wiring 변경은 optional OperationSink, no-op build hook, Router의 optional 내부 SessionID fence뿐이다. SessionID는 wire에 추가되지 않으며 zero 값이면 기존 routing과 같다.

operator-owned **동일 영속 mount**를 모든 시험 Pod/재시작에 유지한다. `LABBIT_C1_DIRECTORY`는 symlink 없는 canonical 절대경로이며 Backend 실행 UID 소유의 `0700` 디렉터리다. `emptyDir`, ephemeral container filesystem, 실행마다 다른 새 경로는 사용할 수 없다. 저장소가 여러 process에서 atomic exclusive create와 fsync를 지원하는지 확인한다. 기존 원장이 있으면 경로를 바꿔 우회하지 않는다.

시험 Pod에만 `LABBIT_C1_HARNESS=WAIT_FOR_OPERATOR`, 위 directory, Downward API `metadata.uid`의 `LABBIT_C1_POD_UID`를 주입한다. 시작 후 최대 15분간 승인 파일을 기다린다. 서비스 readiness는 기존 DB/schema 기준을 유지한다. Harness 실패는 업무 서버를 종료하지 않고 고정 error_code만 기록한다.

operator는 **그 Pod의 로그 및 실제 연결 상태**로 Connector 인증·HELLO_ACK·Heartbeat와 current Session ID를 확인한다. 그 다음 같은 mount에 `approval.json`을 `0600`으로 atomic rename해 제출한다. Backend UID가 파일을 소유해야 한다. 파일 제출이 Mutation gate이므로 후속 명시 승인 전에는 만들지 않는다. 잘못된/만료된 제출도 한 번만 처리하며 수정 파일을 자동 재시도하지 않는다.

정확한 파일 형태는 [c1Plan](../../internal/server/app/router_harness_plan.go)과 [비실환경 fixture](../../internal/server/app/router_harness_test.go)의 `c1TestPlan`을 기준으로 작성한다. fixture의 ID·Pod·CIDR은 실환경에 전용하지 않는다. Secret, raw Provider payload, key 내용, StartupScript는 승인 파일에 넣지 않는다.

| 입력 | operator가 확인할 내용 |
| --- | --- |
| `approval`, `expiresAt` | `PROVISION_AND_EXACT_ID_CLEANUP` 명시 승인, 앞으로 15분 이내의 종료 상한. 승인 시 남은 시간 안에 Cleanup까지 가능해야 한다. |
| `podName`, `podUid`, `connectorId`, `sessionId` | 실제 Pod hostname/UID, 기존 인증 Connector identity, 해당 process의 protocol-ready Session. |
| `projectId`, `evidenceSha256` | 같은 KT Project의 read-only preflight 보고서와 그 SHA256. 보고서는 runtime artifact/config fingerprint, ProviderConnection affinity, Tier networkId/refId, SSH source/key/known_hosts, cost/time cap, quota 제한, Cleanup 담당·허가 범위를 포함한다. |
| `providerRuntimeReady`, `linuxImageVerified`, `rootGiB`, `costAndQuotaApproved`, `cleanupApproved` | 전부 확인/승인되어야 한다. root는 50GiB. Credential과 private key는 Connector 로컬 보호 경계에만 존재한다. |
| `baselineComplete`, `baseline` | SERVER/VOLUME/KT_TIER/KT_FIREWALL_POLICY/PUBLIC_IP/PORT_FORWARD/KEYPAIR 전체 exact-ID inventory. 각 key와 배열 필수, 미조회 결과를 빈 배열로 쓰지 않는다. 기존 공유 자산·Connector VM 보존 목록이다. |
| `existingTierCidrs`, `labCidr`, `managementCidr` | baseline Tier마다 현재 CIDR, 서로와 baseline 모두 겹치지 않는 canonical IPv4 /24 두 개. 승인 파일 값은 준비된 Connector Runtime 값과 일치해야 한다. |
| `creationSnapshot` | resolved ProviderConnection/Image/Flavor UUID와 FlavorSpec, VMKey/role/workspaceVMKey=`workspace`, VM 1개/index 0, internetOutbound=false, StartupScript 없음. |

Backend는 원격 Connector의 설정 파일이나 실제 7종 inventory를 직접 읽을 수 없다. 이 항목들은 operator attestation이며 `evidenceSha256`에 대응하는 실측 보고서를 먼저 확보해야 한다. Connector의 기존 KT Provider preflight가 실제 Project affinity·키 일치·quota·CIDR 충돌 등을 별도로 검증한다. bool만 채워 준비 완료를 추정하지 않는다.

## 단발 실행과 중단

`attempt.jsonl`을 `O_EXCL`로 생성하고 directory sync 후, 시도·승인 digest·fresh operation/lab/generation=1을 먼저 기록한다. 기존 파일(빈/UNKNOWN/완료 포함) 또는 symlink가 있으면 채택·덮어쓰기·재시작하지 않는다. 원장 교체/권한 변경·write/fsync 실패 시 후속 command를 중단한다. operator는 파일과 mount를 유지하고 삭제/교체하지 않아야 한다.

각 Mutation의 intent를 fsync한 뒤 기존 `SendOperationCommand`를 한 번 호출한다. Router가 만든 messageId와 typed ACK/PROGRESS/RESULT의 replyToMessageId, 인증 ConnectorID, operationId/labInstanceId/generation, requestId를 대조한다. ACK 수락만으로 Provider 성공을 판정하지 않는다. 원장에는 correlation, 고정 stage/outcome, 검증한 typed exact-ID만 기록하며 오류 message·raw payload·StartupScript는 기록하지 않는다. 결과 불명/중단 직전에 이미 받은 안전한 ID도 보존한다.

지원 경로는 Provision → known-ID Reconcile → Cleanup → 최종 known-ID Reconcile 한 번뿐이다. Provision 결과가 baseline과 겹치거나 5개 typed ownership reference가 불완전하면 Cleanup을 보내지 않는다. Reconcile은 `discoverCandidates=false`이며 exact ID/type/generation/logicalName/KNOWN_RESOURCE/state가 모두 맞아야 한다. Cleanup에는 검증한 같은 reference만 전달하고 반환 ID도 대조한다. Provider의 기존 삭제 순서·soft/force-delete·부재 확인을 재사용한다.

모든 write에서 Router의 `WithReadyRoute` fence **안에서** SessionID를 비교한다. 사전 확인과 write 사이의 교체도 새 Session에 송신되지 않는다. 기다리는 중 연결 부재·교체, UNKNOWN/FAILED, reject ACK, correlation 오류, timeout, mailbox overflow, 원장 오류이면 자동 Mutation/자동 Cleanup을 하지 않는다. 원장과 local Provider evidence를 보존해 read-only Reconcile/수동 확인으로 넘긴다. 기존 원장의 resume/recovery 실행기는 이 Harness 범위 밖이다.

최종 `EXACT_ID_ABSENCE_CONFIRMED_INVENTORY_COMPARISON_PENDING`은 알려진 5종 reference의 부재 확인이다. **전체 7종 최종 inventory와 baseline 비교, 공유 자산 보존, 비용 종료는 별도 실측**해야 한다. C1 PASS/residual 0/durable Operation 완료를 이 stage만으로 주장하지 않는다.

## 같은 Pod/Session 배포와 복구 시나리오 — 미수행

현재 preflight의 Backend는 1 replica지만 RollingUpdate 중 두 Pod가 동시에 존재할 수 있다. 후속 GitOps 승인은 이를 처리할 rollout 방식, WSS drain/affinity, 시험 Pod의 정확한 owner, immutable 시험 artifact, 영속 원장 mount를 함께 정해야 한다. 다른 Pod에 연결된 Session이면 Harness는 local Registry에서 찾지 못해 fail-closed한다. 다른 Pod로 라우팅해 실행을 강제하거나 새 Router를 만들지 않는다. operator가 같은 시험 Pod에 실제 protocol-ready Session이 있음을 확인한 뒤만 gate를 제출한다. shared O_EXCL ledger와 Pod UID pin은 중복 attempt도 막는다.

승인된 GitOps desired-state 변경으로만 시험 image/mount/config를 적용한다. Argo automated self-heal과 충돌하는 `set image`/수동 patch는 하지 않는다. 기존 Browser/Control/Data WSS는 rollout drain·재접속·일시 서비스 중단의 영향을 받는다. Connector의 Provider Runtime 준비와 기존 identity를 유지한 재시작도 별도 승인 대상이다.

복구는 승인된 GitOps 변경을 되돌려 원래 image digest/배포 설정을 복원하고 readiness·HELLO·Heartbeat를 확인한다. preflight의 원본 digest는 `sha256:e5c6f01ab430f44b028f0a5d31d533d11751b2e7b6941b848a14cdf9a5728997`이며 적용 직전에 실제 Runtime을 재확인한다. 원장과 Provider 설정은 보존한다. 배포 rollback은 Cloud Cleanup 증거가 아니며 UNKNOWN/중단된 시도는 재생성하지 않는다.

Cloud gate 전에 여전히 Provider Credential/config·ProviderConnection 등록·Ed25519 key/known_hosts·현재 baseline/공유 exact ID·두 CIDR·Image/Flavor·cost/time/quota·Cleanup 책임 및 권한·원장 persistence·배포/복구 승인과 실제 Runtime 재확인이 필요하다.
