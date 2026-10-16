# LBT-179 구현·검증 및 실환경 시험 준비

이번 제출은 PostgreSQL/Mock Reconciliation 및 실제 시험 준비 범위다. 실제 KT Cloud Lifecycle Acceptance는 미수행이다. 독립 ChatGPT Review Gate로 반환하며 PR/ GitOps merge, Worker 활성화, Backend 배포, 공유 DB migration, Connector 교체/재설치, Cloud Mutation, Parent LBT-18 Done 처리를 하지 않았다.

## 기준과 보존

- 사용자 TARGET, Jira LBT-179 Acceptance, 저장소 AGENTS.md / CONTRIBUTING.md / TESTING.md를 적용했다. Engineering Master는 별도로 제공되지 않았으며, 사용자가 추가 탐색 없이 이 기준으로 진행하도록 확인했다.
- 기존 `/home/imcherry/orca/workspaces/labbit-app/lbt-18`의 tracked/untracked 변경은 없었다. 기존 `feat/LBT-177-provision-worker-dispatch`와 HEAD `dbcb08766176236fe0577aa0ed0edd2277d9f1da`의 세 커밋을 그대로 보존했다. Squash된 main과 patch ID가 다르다는 이유로 삭제하거나 재적용하지 않았다.
- 요청한 baseline `bb2c25df74644a4ca49ac03772ba8aba91f4e8f8`에서 `feat/LBT-179-reconciliation`을 만들고 해당 worktree를 재사용했다. 세션의 `siren`과 다른 작업 worktree는 변경하지 않았다.

## 구현과 Self-review

실행 의미의 SSOT는 `runtime/contract.yaml`의 `saas.worker`, wire는 기존 `contracts/connector/`다. 새 메시지·HTTP field·DB migration·Scheduler·Queue·별도 Worker Deployment는 없다.

기존 Worker의 claim commit → lease 재검증 → lifecycle context → execute 경계에 조회 전용 store adapter를 연결했다. DB가 UNKNOWN wire 결과를 보존하는 RECONCILING과 만료된 RUNNING만 새로운 observation lease로 처리한다. 정상 mutation claim/renew 및 writer는 RECONCILING을 mutation 실행 권한으로 취급하지 않는다.

Self-review는 다음 경계를 직접 대조했다.

- Item/Operation/Instance/Execution/Snapshot/Connection/Connector 관계와 Snapshot의 connection ID, DB clock 기준의 lock 이후 lease 검증, 최종 write 시 expiry 재검증.
- 외부 I/O 전에 transaction commit, 재시작 후 새 owner, stale mutation/observation writer와 renewal 차단, `FOR UPDATE ... SKIP LOCKED` 경쟁.
- 저장된 exact type/ID/generation/logicalName 집합의 재검증. discovery candidate 또는 모르는 ID를 INSERT/채택/삭제하지 않음. 조회 오류·누락·중복·세대/소유관계 불일치에 대한 HOLD.
- Cleanup에서 과거 DELETED/MISSING도 조회하여 잔여 자원을 PRESENT로 되돌릴 수 있음. 확인된 전체 추적 자원 부재와 오류/후보 없음이 입증될 때만 기존 Cleanup 집계 경계를 사용함.
- Provision/Reset은 관측만으로 생성의 완전성·readiness를 증명하지 못하므로 HOLD. Reset generation을 임의 전환하지 않음. Provider mutation 자동 재실행 없음.
- ACK/RESULT와 Reconcile inbox를 분리하고 Router reply/correlation 검증을 재사용함. Reconcile 응답 유실도 HOLD이며 pending을 해제함.
- confirmed absence 관측이 이전 `deleted_at` 이력을 지우던 부분을 수정했다. 실제 presence 재관측 시에만 해제하며 회귀 테스트로 검증했다.
- raw Provider 오류/credential/Terminal 본문은 새 경로의 log·HTTP에 넣지 않음.

## 로컬 검증

폐기용 Docker PostgreSQL 16과 실제 Control Router/WSS + Mock Provider를 사용했다. `postgrestest`의 격리 DB에 기존 migration을 적용했으며 공유 DB에 쓰지 않았다. 전체 Failure Matrix와 Browser E2E는 추가하지 않았다.

| 검증 | 결과 |
| --- | --- |
| `make setup` | PASS |
| `make test` | 기존 Connector Terminal `TestSessionReleasesUnreadDataConnectionOnReplaceAndDetach/replace` 실패로 중단 |
| 깨끗한 baseline archive에서 위 Terminal test 단독 실행 | 동일 assertion/관측으로 FAIL. 이번 변경에서 Terminal을 수정하지 않음 |
| `make go-fmt-check go-vet go-build web-typecheck web-lint web-test web-build` | PASS. 기존 Web bundle size warning |
| 최종 `go test ./internal/server/... ./internal/postgres/...` | PASS |
| Contracts workflow의 OpenAPI / JSON Schema / Runtime·Migration validator 실행 | PASS, workflow와 같은 고정 validator 버전 |
| PostgreSQL/Mock Reconciliation 및 응답 유실·전송 불명 회귀 | PASS |
| `go test -race -tags 'backend_router_harness integration' -run '^Test(C1\|Reconciliation\|ProvisionControlStack(Ambiguous\|ResponseLoss))' -count=1 -v ./internal/server/app` | PASS. Mock만 실행 |
| 최종 삭제 이력 수정 후 Cleanup 전체 5개 대표 사례 `-race` | PASS |
| 저장소 전체 `make go-integration-test` | 실행 결과를 최종 제출에서 갱신 |

핵심 테스트는 presence/absence, lookup error, 후보 발견, 일부 관측 누락, 잘못된 generation/logicalName, 중복 관측, 응답 유실/late result, Reconcile 응답 유실, Worker 재시작/lease 만료, 동시 claim, stale owner fencing, Cleanup 잔여 자원/후보/불확실성 후 확인된 부재, Reset generation 유지 및 중복 Mutation 방지를 검증한다.

로컬 로그: `/home/imcherry/labbit-evidence/LBT-179/`. `default.log`와 `baseline-terminal.log`에 기존 실패를 보존했다. 범위 외 실패를 숨기기 위해 테스트를 skip하거나 기준을 완화하지 않았다. 최종 HEAD의 원격 CI 근거는 PR checks와 최종 제출을 참조한다.

## 실제 환경 Read-only 확인

2026-10-16 18:5x~19:03 KST의 SSH/Kubernetes 조회다. [클러스터 snapshot](cluster.txt), [공유 DB 조회 결과](shared-db.jsonl), [실행 binary SHA-256](connector-build.txt)을 첨부한다. DSN/credential/key 원문은 출력하거나 다른 환경으로 복사하지 않았다. DB credential은 관리 서버 내 프로세스 메모리에서만 사용했고 DB session의 `transaction_read_only=on`을 확인했다. SQL은 SELECT만 수행했다.

| 항목 | 관측 |
| --- | --- |
| Backend | `ghcr.io/ktcloud4-sl/backend:sha-bb2c25d`, digest `sha256:30195249ed341820e125d6bb51f830d4aecffa36c24035ba94739a990dc32ec7` |
| Readiness/Argo | 최초 Pod 2/2 Ready, `/readyz` = `ok`. 최신 snapshot에서도 app container ready, Backend Argo Synced/Healthy |
| Argo revision | 최신 snapshot `8154923420471c55a3faaae6e63f4b81ec12b377`. 조회 도중 다른 변경으로 revision이 바뀌었으므로 시험 직전에 재확인 필요 |
| Cluster 참고 | 최초 `platform-networking` OutOfSync/Healthy, applicationset-controller 0/1. Backend 앱은 Healthy; 원인 조사/수정은 수행하지 않음 |
| Runtime roles | `LABBIT_RUNTIME_ROLES=api,realtime`; Worker 비활성 |
| PostgreSQL | 16.15. migration 1~9 checksum 전부 현재 Git과 일치. 새 migration 불필요 |
| 기존 작업 위험 | 마지막 조회에서 operations / operation_items / provider_resources 전부 0건. 이후 사용자 요청으로 변할 수 있으므로 Worker 활성화 직전 재조회 필요 |
| Connector VM | 기존 PID 8869, 10/15부터 실행, `/home/ubuntu/labbit-c1/bin/labbit-connector-ebe3f42`. 실행 SHA-256 `2080db4945b9d8e97a9e307772ffb12b15c697cb5f1c04df674232619de202f0` |
| Connector 연결 | 기존 ID `7a31cea7-44b4-474b-bbde-dde650c22768`, 미폐기, credential 1개 활성. 19:03:16 조회 시 last_seen 19:03:10. VM에 443 ESTABLISHED도 확인 |
| Connector 버전 한계 | DB `last_connector_version`은 NULL, 실행 binary의 VCS build metadata도 확인되지 않음. 파일명만으로 현재 source commit·최신 기능 일치를 보증하지 않음 |
| LBT-167 준비 재사용 | 기존 Provider env/SSH key·known_hosts/config 파일과 identity 경로가 남아 있고 실행 process에 Provider env가 적용됨. 원문은 읽어 내보내지 않음 |
| ProviderConnection | VM의 설정 ID `9128f839-a8a0-403b-871c-a387b162ff06`; 공유 DB의 provider_connections는 **0건**, 현재 재사용 가능한 등록 row 없음 |

기존 VM의 `provider-verify.json`은 2026-10-15 02:20:34 UTC의 `validateConnection=PASS`, `baselineMatchesExpected=true`, `saasProviderConnectionRegistered=false`, `labMutation=false`를 기록한다. 이는 저장된 과거 evidence이며 이번에 실제 Provider 인증/쿼터/inventory를 새로 확인한 결과는 아니다. 현 Connector 연결을 유지했고 재설치·교체·재시작하지 않았다.

## 승인 후 실제 Lifecycle 1회에 필요한 최소 변경·위험

1. 독립 Review Gate 뒤 승인된 immutable Backend image로 기존 단일 Backend를 배포하고 같은 process에 `worker` role을 추가한다(`api,realtime,worker`). DB migration과 별도 Worker Deployment는 필요하지 않다.
2. 기존 LBT-167 VM·Connector identity·Provider 설정을 보존하며 시험 대상 ProviderConnection 등록과 Image/Flavor mapping·Snapshot/시험 LabSpec을 준비해야 한다. 현재는 등록 row가 없어 HTTP Lifecycle을 시작할 준비가 끝난 상태가 아니다. binary의 기존 wire/Provider 호환성은 사전 확인 대상이며 교체 승인을 가정하지 않는다.
3. Worker는 등록된 모든 eligible PENDING 작업을 처리하므로 활성화 직전 backlog·target ownership을 다시 확인하고, 승인한 시험 이외 요청 유입을 통제해야 한다. RollingUpdate 중 Connector가 다른 Pod의 Registry에 붙으면 무전송 defer/HOLD가 발생할 수 있어 단일 시험 process/연결 소유를 확인한다.
4. 현재 Project inventory·공유 exact IDs·CIDR·Image/Flavor·quota/비용/시간 한도·Cleanup 책임을 재확인한다. Provision/Reset UNKNOWN은 안전하게 HOLD될 수 있다. HOLD 해소 없이 새 Mutation을 강행하지 않으며 DB 기록 부재를 Cloud 잔여 자원 부재로 해석하지 않는다.
5. 승인된 시험은 HTTP → durable DB → Worker → 기존 Connector → KT Cloud의 Provision → Reset → Reconcile → Cleanup 한 번만 수행하고, generation/명령 correlation/known-ID 최종 부재/기존 공유 자원 보존을 별도 실측한다. READY·C2 SSH/Terminal 및 전체 Acceptance 완료는 이번 결과로 주장하지 않는다.

위 변경과 실제 시험은 실행하지 않았다. 배포 rollback은 Provider Cleanup을 대신하지 않으며, 결과 불명 상태는 관측과 별도 승인으로 처리해야 한다.
